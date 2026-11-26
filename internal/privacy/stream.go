package privacy

import (
    "bytes"
    "encoding/json"
    "strings"
    "unicode/utf8"

    llm "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
)

type streamObject = map[string]json.RawMessage
type streamPath = []any

type streamKey struct {
    group, field                string
    choice, item, content, tool int
}

// StreamCommit 是可写回的一份原始事件及其改写结果. 调用者在实际写入后记录 Changed.
type StreamCommit struct {
    Event   *httpclient.StreamEvent
    Changed bool
}

// StreamRestorer 由单一读取方按事件顺序使用, 固定持有请求快照.
type StreamRestorer struct {
    snapshot      *Snapshot
    format        llm.APIFormat
    classifier    streamClassifier
    policy        FailurePolicy
    queue         []*streamEntry
    originalBytes int
    outputBytes   int
    outputLimit   bool
    sequence      uint64
    streams       map[streamKey]*streamText
    terminal      bool
    bypass        bool
    validateWire  bool
    closed        bool
}

type streamEntry struct {
    original      *httpclient.StreamEvent
    sequence      uint64
    commitThrough uint64
    originalBytes int
    pending       int
    fields        []*streamField
    patches       []streamPatch
}

type streamPatch struct {
    path  streamPath
    value json.RawMessage
}

type streamField struct {
    owner  *streamEntry
    stream *StreamRestorer
    path   streamPath
    input  string
    output strings.Builder
}

func (field *streamField) retain() {
    if field.owner != nil {
        field.owner.pending++
    }
}

func (field *streamField) release() {
    if field.owner != nil {
        field.owner.pending--
    }
}

func (field *streamField) write(text string) {
    if s := field.stream; s != nil {
        if s.outputLimit || len(text) > MaxPendingBytes-s.originalBytes-s.outputBytes {
            s.outputLimit = true
            return
        }
        s.outputBytes += len(text)
    }
    field.output.WriteString(text)
}

type ownedPiece struct {
    field *streamField
    text  string
}

// 在同一标记的恢复文本和被消费尾部之间保留提交依赖. 如果尾部事件还有另一个
// 未决字段, 也要等该事件就绪, 避免继续策略回退时发出半组改写.
type ownedRune struct {
    pieces []ownedPiece
    single [1]ownedPiece
}

type ownedRestorer struct {
    text    *Restorer
    escape  bool
    pending []ownedRune
    head    int
}

func newOwnedRestorer(snapshot *Snapshot, escape bool) *ownedRestorer {
    return &ownedRestorer{text: NewRestorer(snapshot), escape: escape}
}

func (restorer *ownedRestorer) pushRune(decoded string, pieces []ownedPiece) {
    if !restorer.text.snapshot.Active() || len(decoded) != 1 || !restorer.text.HasPending() && decoded[0] != MarkerLeft[0] {
        restorer.text.flushLiteral(restorer.literal)
        for _, piece := range pieces {
            piece.field.write(piece.text)
        }
        restorer.compact()
        return
    }
    for _, piece := range pieces {
        piece.field.retain()
    }
    restorer.pending = append(restorer.pending, ownedRune{})
    last := &restorer.pending[len(restorer.pending)-1]
    if len(pieces) == 1 {
        last.single[0] = pieces[0]
    } else {
        last.pieces = append([]ownedPiece(nil), pieces...)
    }
    restorer.text.feed(decoded[0], restorer.literal, restorer.replace)
    restorer.compact()
}

func (runeOwner *ownedRune) fragments() []ownedPiece {
    if runeOwner.pieces != nil {
        return runeOwner.pieces
    }
    return runeOwner.single[:]
}

func (restorer *ownedRestorer) literal(decoded string) {
    restorer.consume(len(decoded), true)
}

func (restorer *ownedRestorer) replace(length int, source string) {
    first := restorer.pending[restorer.head].fragments()[0].field
    if restorer.escape {
        encoded, _ := json.Marshal(source)
        source = string(encoded[1 : len(encoded)-1])
    }
    first.write(source)
    if entry := first.owner; entry != nil {
        for index := restorer.head; index < restorer.head+length; index++ {
            for _, piece := range restorer.pending[index].fragments() {
                if other := piece.field.owner; other != nil && other.sequence > entry.commitThrough {
                    entry.commitThrough = other.sequence
                }
            }
        }
    }
    restorer.consume(length, false)
}

func (restorer *ownedRestorer) consume(length int, asLiteral bool) {
    for index := restorer.head; index < restorer.head+length; index++ {
        for _, piece := range restorer.pending[index].fragments() {
            if asLiteral {
                piece.field.write(piece.text)
            }
            piece.field.release()
        }
        restorer.pending[index] = ownedRune{}
    }
    restorer.head += length
}

func (restorer *ownedRestorer) compact() {
    if restorer.head == 0 {
        return
    }
    remaining := copy(restorer.pending, restorer.pending[restorer.head:])
    clear(restorer.pending[remaining:])
    restorer.pending = restorer.pending[:remaining]
    restorer.head = 0
}

func (restorer *ownedRestorer) finish() {
    restorer.text.flushLiteral(restorer.literal)
    restorer.text.Finish()
    restorer.pending = nil
    restorer.head = 0
}

func (restorer *ownedRestorer) pendingBytes() int {
    total := len(restorer.text.candidate) + restorer.text.prefixLength
    for index := restorer.head; index < len(restorer.pending); index++ {
        for _, piece := range restorer.pending[index].fragments() {
            total += len(piece.text)
        }
    }
    return total
}

type streamText struct {
    plain    *ownedRestorer
    embedded *jsonStream
}

func NewStreamRestorer(snapshot *Snapshot, format llm.APIFormat) *StreamRestorer {
    active := snapshot.Active()
    restorer := &StreamRestorer{snapshot: snapshot, format: format, bypass: !active, validateWire: active, policy: Continue}
    if snapshot != nil {
        restorer.policy = snapshot.Config().FailurePolicy
    }
    return restorer
}

// Push 返回 FIFO 中可提交的事件. Continue 触发时同时返回原样事件及固定错误,
// 调用者应写出事件并记录处理失败. Closed 为 true 时不能再写入事件.
func (s *StreamRestorer) Push(event *httpclient.StreamEvent) ([]StreamCommit, error) {
    if s.closed || event == nil {
        return nil, ErrStreamIdentity
    }
    if !s.validateWire {
        return []StreamCommit{{Event: event}}, nil
    }

    done := s.format == llm.APIFormatOpenAIChatCompletion && bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]"))
    hasJSON := !done && len(event.Data) != 0
    if hasJSON && (rawKind(event.Data) != '{' || !utf8.Valid(event.Data) || !json.Valid(event.Data)) {
        s.Cancel()
        return nil, ErrInvalidJSON
    }
    if s.bypass {
        // 继续策略停止字段恢复, 但不把后续非法外层事件发送给客户端.
        return []StreamCommit{{Event: event}}, nil
    }
    eventBytes := streamEventBytes(event)
    if len(s.queue) == MaxPendingEvents || eventBytes > MaxPendingBytes-s.pendingBytes() {
        return s.fail(ErrResourceLimit, event)
    }
    ownedEvent := *event
    ownedEvent.Data = bytes.Clone(event.Data)
    ownedEvent.Headers = event.Headers.Clone()
    s.sequence++
    entry := &streamEntry{original: &ownedEvent, sequence: s.sequence, originalBytes: eventBytes}
    s.queue = append(s.queue, entry)
    s.originalBytes += eventBytes

    var err error
    if done {
        err = s.classifier.terminate(s, false)
    } else if hasJSON {
        // 严格验证字符串和深度后再用 map 解码, 避免非法键被静默修复.
        if err = validatePrivacyJSON(event.Data); err == nil {
            var root streamObject
            root, err = decodeObject(event.Data)
            if err == nil {
                err = s.classifier.classify(s, entry, root)
            }
        }
    }
    if err != nil {
        return s.fail(err, nil)
    }
    if s.outputLimit || s.pendingBytes() > MaxPendingBytes {
        return s.fail(ErrResourceLimit, nil)
    }
    return s.drain()
}

// Finish 用于正常 EOF 和上游读取错误的收尾. 终止事件由上游提供, 不补造.
func (s *StreamRestorer) Finish() ([]StreamCommit, error) {
    if s.closed {
        return nil, nil
    }
    if err := s.finishStreams(nil, true); err != nil {
        commits, failure := s.fail(err, nil)
        s.Cancel()
        return commits, failure
    }
    if s.outputLimit || s.pendingBytes() > MaxPendingBytes {
        commits, failure := s.fail(ErrResourceLimit, nil)
        s.Cancel()
        return commits, failure
    }
    commits, err := s.drain()
    s.Cancel()
    return commits, err
}

func (s *StreamRestorer) Closed() bool { return s.closed }

func (s *StreamRestorer) Cancel() {
    s.clearPending()
    s.snapshot = nil
    s.closed = true
}

func (s *StreamRestorer) clearPending() {
    s.queue = nil
    s.originalBytes = 0
    s.outputBytes = 0
    s.outputLimit = false
    s.streams = nil
    s.classifier = streamClassifier{}
}

func (s *StreamRestorer) fail(err error, extra *httpclient.StreamEvent) ([]StreamCommit, error) {
    if s.policy == Abort {
        s.Cancel()
        return nil, err
    }
    commits := make([]StreamCommit, 0, len(s.queue)+1)
    for _, entry := range s.queue {
        commits = append(commits, StreamCommit{Event: entry.original})
    }
    if extra != nil {
        commits = append(commits, StreamCommit{Event: extra})
    }
    s.clearPending()
    s.snapshot = nil
    s.bypass = true
    return commits, err
}

func streamEventBytes(event *httpclient.StreamEvent) int {
    total := len(event.Data) + len(event.Type) + len(event.LastEventID)
    for key, values := range event.Headers {
        total += len(key)
        for _, value := range values {
            total += len(value)
        }
    }
    return total
}

func (s *StreamRestorer) pendingBytes() int {
    total := s.originalBytes + s.classifier.pendingBytes()
    for _, entry := range s.queue {
        for _, patch := range entry.patches {
            total += len(patch.value)
        }
        for _, field := range entry.fields {
            total += len(field.input) + field.output.Len()
        }
    }
    for key, state := range s.streams {
        total += len(key.group) + len(key.field)
        if state.plain != nil {
            total += state.plain.pendingBytes()
        } else {
            total += state.embedded.pendingBytes()
        }
    }
    return total
}

func (s *StreamRestorer) delta(entry *streamEntry, path streamPath, key streamKey, text string, embeddedJSON bool) error {
    if s.terminal {
        return ErrStreamIdentity
    }
    state, exists := s.streams[key]
    if !exists {
        if len(s.streams) >= MaxLogicalStreams {
            return ErrResourceLimit
        }
        if s.streams == nil {
            s.streams = make(map[streamKey]*streamText)
        }
        state = &streamText{}
        if embeddedJSON {
            state.embedded = newJSONStream(s.snapshot)
        } else {
            state.plain = newOwnedRestorer(s.snapshot, false)
        }
        s.streams[key] = state
    } else if embeddedJSON != (state.embedded != nil) {
        return ErrStreamIdentity
    }
    part := &streamField{owner: entry, stream: s, path: path, input: text}
    entry.fields = append(entry.fields, part)
    if state.embedded != nil {
        return state.embedded.push(text, part)
    }
    for index, current := range text {
        decoded := text[index : index+utf8.RuneLen(current)]
        state.plain.pushRune(decoded, []ownedPiece{{field: part, text: decoded}})
    }
    return nil
}

func (s *StreamRestorer) finishStreams(predicate func(streamKey) bool, allowIncomplete bool) error {
    for key, state := range s.streams {
        if predicate != nil && !predicate(key) {
            continue
        }
        if state.embedded != nil {
            if err := state.embedded.finish(allowIncomplete); err != nil {
                return err
            }
        } else {
            state.plain.finish()
        }
        delete(s.streams, key)
    }
    return nil
}

func (s *StreamRestorer) snapshotField(entry *streamEntry, path streamPath, raw json.RawMessage, selector string) error {
    updated, changed, err := rewriteSnapshot(s.snapshot, raw, selector)
    if err == nil && changed {
        entry.patches = append(entry.patches, streamPatch{path: path, value: updated})
    }
    return err
}

func (s *StreamRestorer) completeText(entry *streamEntry, path streamPath, raw json.RawMessage, embeddedJSON bool) error {
    text, err := DecodeJSONString(raw)
    if err != nil {
        return err
    }
    part := &streamField{stream: s, path: path, input: text}
    if embeddedJSON {
        embedded := newJSONStream(s.snapshot)
        if err := embedded.push(text, part); err != nil {
            return err
        }
        if err := embedded.finish(false); err != nil {
            return err
        }
    } else {
        plain := NewRestorer(s.snapshot)
        initial, _ := plain.Push(text)
        tail, _ := plain.Finish()
        part.write(initial)
        part.write(tail)
    }
    entry.fields = append(entry.fields, part)
    return nil
}

func (s *StreamRestorer) drain() ([]StreamCommit, error) {
    ready := 0
    horizon := uint64(0)
    for index, entry := range s.queue {
        if entry.pending != 0 {
            break
        }
        horizon = max(horizon, entry.commitThrough)
        if entry.sequence >= horizon {
            ready = index + 1
        }
    }
    if ready == 0 {
        return nil, nil
    }
    // 整组编码成功再消费队列. 预算包含编码结果, 以及回退时需要的原事件.
    budget := MaxPendingBytes - s.pendingBytes()
    commits := make([]StreamCommit, 0, ready)
    for _, entry := range s.queue[:ready] {
        commit, err := entry.encode()
        if err != nil {
            s.Cancel()
            return nil, err
        }
        if commit.Changed {
            budget -= len(commit.Event.Data)
            if budget < 0 {
                return s.fail(ErrResourceLimit, nil)
            }
        }
        commits = append(commits, commit)
    }
    for _, entry := range s.queue[:ready] {
        s.originalBytes -= entry.originalBytes
        for _, field := range entry.fields {
            s.outputBytes -= field.output.Len()
        }
    }
    clear(s.queue[:ready])
    s.queue = s.queue[ready:]
    if len(s.queue) == 0 {
        s.queue = nil
    }
    return commits, nil
}

func (entry *streamEntry) encode() (StreamCommit, error) {
    data := json.RawMessage(entry.original.Data)
    changed := len(entry.patches) > 0
    patches := entry.patches
    for _, field := range entry.fields {
        if output := field.output.String(); output != field.input {
            encoded, err := EncodeJSONString(output)
            if err != nil {
                return StreamCommit{}, err
            }
            patches = append(patches, streamPatch{path: field.path, value: encoded})
            changed = true
        }
    }
    if !changed {
        return StreamCommit{Event: entry.original}, nil
    }
    tree := &streamPatchTree{}
    for _, patch := range patches {
        tree.insert(patch.path, patch.value)
    }
    data, err := tree.apply(data)
    if err != nil {
        return StreamCommit{}, err
    }
    event := *entry.original
    event.Data = data
    return StreamCommit{Event: &event, Changed: !bytes.Equal(data, entry.original.Data)}, nil
}

// 同一事件可能包含多个 choice 和 tool 字段. 收集改动路径后只重建每个父容器一次.
type streamPatchTree struct {
    value    json.RawMessage
    children map[any]*streamPatchTree
}

func (tree *streamPatchTree) insert(path streamPath, value json.RawMessage) {
    for _, step := range path {
        if tree.children == nil {
            tree.children = make(map[any]*streamPatchTree)
        }
        child := tree.children[step]
        if child == nil {
            child = &streamPatchTree{}
            tree.children[step] = child
        }
        tree = child
    }
    tree.value = value
}

func (tree *streamPatchTree) apply(raw json.RawMessage) (json.RawMessage, error) {
    if tree.value != nil {
        raw = tree.value
    }
    if len(tree.children) == 0 {
        return raw, nil
    }
    switch rawKind(raw) {
    case '{':
        object, err := decodeObject(raw)
        if err != nil {
            return nil, err
        }
        for step, child := range tree.children {
            key, ok := step.(string)
            if !ok || object[key] == nil {
                return nil, ErrInvalidJSON
            }
            object[key], err = child.apply(object[key])
            if err != nil {
                return nil, err
            }
        }
        return marshalJSONObject(object), nil
    case '[':
        array, err := streamArray(raw)
        if err != nil {
            return nil, err
        }
        for step, child := range tree.children {
            index, ok := step.(int)
            if !ok || index < 0 || index >= len(array) {
                return nil, ErrInvalidJSON
            }
            array[index], err = child.apply(array[index])
            if err != nil {
                return nil, err
            }
        }
        var encoded bytes.Buffer
        encoded.WriteByte('[')
        for index, item := range array {
            if index > 0 {
                encoded.WriteByte(',')
            }
            encoded.Write(item)
        }
        encoded.WriteByte(']')
        return encoded.Bytes(), nil
    default:
        return nil, ErrInvalidJSON
    }
}
