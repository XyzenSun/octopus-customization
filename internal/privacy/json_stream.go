package privacy

import (
    "unicode/utf16"
    "unicode/utf8"
)

// jsonStream 校验单个内层 JSON 文档, 不缓存或反复解码整篇文档.
// 仅解码后的字符串值进入 ownedRestorer.
// 语法和未修改的词法单元保留在原 streamField 中.
// 调用方保证事务性: push 出错使当前整个 FIFO 候选失效,
// 包括本次 push 中已完成的写入.
type jsonStream struct {
    snapshot *Snapshot
    stack    []jsonStreamFrame
    rootDone bool
    sawBytes bool
    finished bool
    err      error

    token       jsonStreamToken
    number      jsonStreamNumber
    literal     string
    literalNext int
    stringKey   bool
    restorer    *ownedRestorer

    // 此处仅保留未完成的 UTF-8 rune/转义 (最多 12 字节).
    // 每个片段持有一次 retain; pushRune 在其释放前自行 retain.
    // 未修改的转义逐字节回放, 不重新编码.
    lexMode  jsonStreamLexMode
    lexBytes int
    pieces   []ownedPiece
    code     uint16
    high     uint16
    hexCount int
    utf8Size int
    utf8Lead byte
    utf8Rune rune
}

type jsonStreamFrame struct {
    kind   byte
    expect jsonStreamExpectation
}

type jsonStreamExpectation uint8

const (
    jsonStreamObjectKeyOrEnd jsonStreamExpectation = iota
    jsonStreamObjectKey
    jsonStreamObjectColon
    jsonStreamObjectValue
    jsonStreamObjectCommaOrEnd
    jsonStreamArrayValueOrEnd
    jsonStreamArrayValue
    jsonStreamArrayCommaOrEnd
)

type jsonStreamToken uint8

const (
    jsonStreamNoToken jsonStreamToken = iota
    jsonStreamString
    jsonStreamNumeric
    jsonStreamLiteral
)

type jsonStreamNumber uint8

const (
    jsonStreamNumberSign jsonStreamNumber = iota
    jsonStreamNumberZero
    jsonStreamNumberInteger
    jsonStreamNumberFractionFirst
    jsonStreamNumberFraction
    jsonStreamNumberExponentFirst
    jsonStreamNumberExponentSign
    jsonStreamNumberExponent
)

type jsonStreamLexMode uint8

const (
    jsonStreamLexNormal jsonStreamLexMode = iota
    jsonStreamLexEscape
    jsonStreamLexUnicode
    jsonStreamLexLowSlash
    jsonStreamLexLowU
    jsonStreamLexLowUnicode
    jsonStreamLexUTF8
)

func newJSONStream(snapshot *Snapshot) *jsonStream {
    return &jsonStream{snapshot: snapshot}
}

func (s *jsonStream) push(text string, owner *streamField) error {
    if s.err != nil {
        return s.err
    }
    if s.finished {
        return ErrStreamIdentity
    }
    if len(text) > 0 {
        s.sawBytes = true
    }
    for i := 0; i < len(text); {
        current := text[i]
        consumed := true
        var err error
        switch s.token {
        case jsonStreamString:
            err = s.pushStringByte(current, owner)
        case jsonStreamNumeric:
            consumed, err = s.pushNumberByte(current, owner)
        case jsonStreamLiteral:
            if current != s.literal[s.literalNext] {
                err = ErrInvalidJSON
            } else {
                owner.write(text[i : i+1])
                s.literalNext++
                if s.literalNext == len(s.literal) {
                    s.token = jsonStreamNoToken
                    s.completeValue()
                }
            }
        default:
            err = s.pushSyntaxByte(current, owner)
        }
        if err != nil {
            return s.fail(err)
        }
        if consumed {
            i++
        }
    }
    return nil
}

func (s *jsonStream) pushSyntaxByte(current byte, owner *streamField) error {
    if current == ' ' || current == '\t' || current == '\r' || current == '\n' {
        owner.write(string(current))
        return nil
    }
    if len(s.stack) == 0 {
        if s.rootDone {
            return ErrInvalidJSON
        }
        return s.startValue(current, owner)
    }
    frame := &s.stack[len(s.stack)-1]
    switch frame.expect {
    case jsonStreamObjectKeyOrEnd, jsonStreamObjectKey:
        if current == '}' && frame.expect == jsonStreamObjectKeyOrEnd {
            return s.closeContainer(current, owner)
        }
        if current != '"' {
            return ErrInvalidJSON
        }
        s.startString(true, owner)
    case jsonStreamObjectColon:
        if current != ':' {
            return ErrInvalidJSON
        }
        frame.expect = jsonStreamObjectValue
        owner.write(":")
    case jsonStreamObjectValue, jsonStreamArrayValue:
        return s.startValue(current, owner)
    case jsonStreamArrayValueOrEnd:
        if current == ']' {
            return s.closeContainer(current, owner)
        }
        return s.startValue(current, owner)
    case jsonStreamObjectCommaOrEnd, jsonStreamArrayCommaOrEnd:
        if current == ',' {
            if frame.kind == '{' {
                frame.expect = jsonStreamObjectKey
            } else {
                frame.expect = jsonStreamArrayValue
            }
            owner.write(",")
            return nil
        }
        return s.closeContainer(current, owner)
    }
    return nil
}

func (s *jsonStream) startValue(current byte, owner *streamField) error {
    switch current {
    case '{', '[':
        if len(s.stack) == MaxJSONDepth {
            return ErrResourceLimit
        }
        // 父容器已被占用, 须等新压入的容器关闭后
        // 才能接收下一个 token.
        s.completeValue()
        expect := jsonStreamObjectKeyOrEnd
        if current == '[' {
            expect = jsonStreamArrayValueOrEnd
        }
        s.stack = append(s.stack, jsonStreamFrame{kind: current, expect: expect})
        owner.write(string(current))
    case '"':
        s.startString(false, owner)
    case 't', 'f', 'n':
        s.token = jsonStreamLiteral
        s.literalNext = 1
        switch current {
        case 't':
            s.literal = "true"
        case 'f':
            s.literal = "false"
        case 'n':
            s.literal = "null"
        }
        owner.write(string(current))
    case '-':
        s.token = jsonStreamNumeric
        s.number = jsonStreamNumberSign
        owner.write("-")
    default:
        if current < '0' || current > '9' {
            return ErrInvalidJSON
        }
        s.token = jsonStreamNumeric
        s.number = jsonStreamNumberInteger
        if current == '0' {
            s.number = jsonStreamNumberZero
        }
        owner.write(string(current))
    }
    return nil
}

func (s *jsonStream) completeValue() {
    if len(s.stack) == 0 {
        s.rootDone = true
        return
    }
    frame := &s.stack[len(s.stack)-1]
    if frame.kind == '{' {
        frame.expect = jsonStreamObjectCommaOrEnd
    } else {
        frame.expect = jsonStreamArrayCommaOrEnd
    }
}

func (s *jsonStream) closeContainer(current byte, owner *streamField) error {
    frame := s.stack[len(s.stack)-1]
    if frame.kind == '{' && current != '}' || frame.kind == '[' && current != ']' {
        return ErrInvalidJSON
    }
    s.stack = s.stack[:len(s.stack)-1]
    owner.write(string(current))
    return nil
}

func (s *jsonStream) numberComplete() bool {
    return s.number == jsonStreamNumberZero || s.number == jsonStreamNumberInteger ||
        s.number == jsonStreamNumberFraction || s.number == jsonStreamNumberExponent
}

// 数字分隔符交回语法处理, 不吞掉.
// 数字无需保留 owner 引用: 校验不会修改其词法单元.
func (s *jsonStream) pushNumberByte(current byte, owner *streamField) (bool, error) {
    digit := current >= '0' && current <= '9'
    switch s.number {
    case jsonStreamNumberSign:
        if !digit {
            return true, ErrInvalidJSON
        }
        s.number = jsonStreamNumberInteger
        if current == '0' {
            s.number = jsonStreamNumberZero
        }
    case jsonStreamNumberZero, jsonStreamNumberInteger:
        switch {
        case digit:
            if s.number == jsonStreamNumberZero {
                return true, ErrInvalidJSON
            }
        case current == '.':
            s.number = jsonStreamNumberFractionFirst
        case current == 'e' || current == 'E':
            s.number = jsonStreamNumberExponentFirst
        default:
            s.token = jsonStreamNoToken
            s.completeValue()
            return false, nil
        }
    case jsonStreamNumberFractionFirst:
        if !digit {
            return true, ErrInvalidJSON
        }
        s.number = jsonStreamNumberFraction
    case jsonStreamNumberFraction:
        if current == 'e' || current == 'E' {
            s.number = jsonStreamNumberExponentFirst
        } else if !digit {
            s.token = jsonStreamNoToken
            s.completeValue()
            return false, nil
        }
    case jsonStreamNumberExponentFirst:
        if current == '+' || current == '-' {
            s.number = jsonStreamNumberExponentSign
        } else if digit {
            s.number = jsonStreamNumberExponent
        } else {
            return true, ErrInvalidJSON
        }
    case jsonStreamNumberExponentSign:
        if !digit {
            return true, ErrInvalidJSON
        }
        s.number = jsonStreamNumberExponent
    case jsonStreamNumberExponent:
        if !digit {
            s.token = jsonStreamNoToken
            s.completeValue()
            return false, nil
        }
    }
    owner.write(string(current))
    return true, nil
}

func (s *jsonStream) startString(key bool, owner *streamField) {
    s.token = jsonStreamString
    s.stringKey = key
    s.lexMode = jsonStreamLexNormal
    if !key {
        s.restorer = newOwnedRestorer(s.snapshot, true)
    }
    owner.write("\"")
}

func (s *jsonStream) pushStringByte(current byte, owner *streamField) error {
    if s.lexMode == jsonStreamLexNormal {
        switch {
        case current == '"':
            if s.restorer != nil {
                s.restorer.finish()
                s.restorer = nil
            }
            owner.write("\"")
            s.token = jsonStreamNoToken
            if s.stringKey {
                s.stack[len(s.stack)-1].expect = jsonStreamObjectColon
            } else {
                s.completeValue()
            }
        case current == '\\':
            s.holdLexByte(current, owner)
            s.lexMode = jsonStreamLexEscape
        case current < 0x20:
            return ErrInvalidJSON
        case current < utf8.RuneSelf:
            text := string(current)
            if s.stringKey {
                owner.write(text)
            } else {
                s.restorer.pushRune(text, []ownedPiece{{field: owner, text: text}})
            }
        default:
            switch {
            case current >= 0xC2 && current <= 0xDF:
                s.utf8Size = 2
            case current >= 0xE0 && current <= 0xEF:
                s.utf8Size = 3
            case current >= 0xF0 && current <= 0xF4:
                s.utf8Size = 4
            default:
                return ErrInvalidJSON
            }
            s.utf8Lead = current
            s.utf8Rune = rune(current & (0x7F >> s.utf8Size))
            s.holdLexByte(current, owner)
            s.lexMode = jsonStreamLexUTF8
        }
        return nil
    }

    switch s.lexMode {
    case jsonStreamLexUTF8:
        if current < 0x80 || current > 0xBF {
            return ErrInvalidJSON
        }
        if s.lexBytes == 1 {
            lead := s.utf8Lead
            if lead == 0xE0 && current < 0xA0 || lead == 0xED && current > 0x9F ||
                lead == 0xF0 && current < 0x90 || lead == 0xF4 && current > 0x8F {
                return ErrInvalidJSON
            }
        }
        s.holdLexByte(current, owner)
        s.utf8Rune = s.utf8Rune<<6 | rune(current&0x3F)
        if s.lexBytes == s.utf8Size {
            s.emitLexRune(string(s.utf8Rune))
        }
    case jsonStreamLexEscape:
        s.holdLexByte(current, owner)
        var decoded string
        switch current {
        case '"', '\\', '/':
            decoded = string(current)
        case 'b':
            decoded = "\b"
        case 'f':
            decoded = "\f"
        case 'n':
            decoded = "\n"
        case 'r':
            decoded = "\r"
        case 't':
            decoded = "\t"
        case 'u':
            s.code, s.hexCount = 0, 0
            s.lexMode = jsonStreamLexUnicode
            return nil
        default:
            return ErrInvalidJSON
        }
        s.emitLexRune(decoded)
    case jsonStreamLexUnicode, jsonStreamLexLowUnicode:
        digit, ok := jsonStreamHexDigit(current)
        if !ok {
            return ErrInvalidJSON
        }
        s.holdLexByte(current, owner)
        s.code = s.code<<4 | digit
        s.hexCount++
        // low surrogate 必须以 DC..DF 开头; 拒绝非法前缀,
        // 防止 allowIncomplete 接受无效 UTF-16.
        if s.lexMode == jsonStreamLexLowUnicode &&
            (s.hexCount == 1 && digit != 0xD || s.hexCount == 2 && digit < 0xC) {
            return ErrInvalidJSON
        }
        if s.lexMode == jsonStreamLexUnicode && s.hexCount == 2 && s.code >= 0xDC && s.code <= 0xDF {
            return ErrInvalidJSON
        }
        if s.hexCount != 4 {
            return nil
        }
        if s.lexMode == jsonStreamLexLowUnicode {
            s.emitLexRune(string(utf16.DecodeRune(rune(s.high), rune(s.code))))
        } else if s.code >= 0xD800 && s.code <= 0xDBFF {
            s.high = s.code
            s.lexMode = jsonStreamLexLowSlash
        } else if s.code >= 0xDC00 && s.code <= 0xDFFF {
            return ErrInvalidJSON
        } else {
            s.emitLexRune(string(rune(s.code)))
        }
    case jsonStreamLexLowSlash:
        if current != '\\' {
            return ErrInvalidJSON
        }
        s.holdLexByte(current, owner)
        s.lexMode = jsonStreamLexLowU
    case jsonStreamLexLowU:
        if current != 'u' {
            return ErrInvalidJSON
        }
        s.holdLexByte(current, owner)
        s.code, s.hexCount = 0, 0
        s.lexMode = jsonStreamLexLowUnicode
    }
    return nil
}

func jsonStreamHexDigit(current byte) (uint16, bool) {
    switch {
    case current >= '0' && current <= '9':
        return uint16(current - '0'), true
    case current >= 'a' && current <= 'f':
        return uint16(current - 'a' + 10), true
    case current >= 'A' && current <= 'F':
        return uint16(current - 'A' + 10), true
    default:
        return 0, false
    }
}

func (s *jsonStream) holdLexByte(current byte, owner *streamField) {
    s.lexBytes++
    // byte 直接转为 string 会编码为 U+00xx, 破坏原 UTF-8
    // 词法单元; 因此保留单个原始字节.
    text := string([]byte{current})
    if n := len(s.pieces); n > 0 && s.pieces[n-1].field == owner {
        s.pieces[n-1].text += text
        return
    }
    owner.retain()
    s.pieces = append(s.pieces, ownedPiece{field: owner, text: text})
}

func (s *jsonStream) emitLexRune(decoded string) {
    if s.stringKey {
        for _, piece := range s.pieces {
            piece.field.write(piece.text)
        }
    } else {
        s.restorer.pushRune(decoded, s.pieces)
    }
    s.releaseLex()
    s.lexMode = jsonStreamLexNormal
}

func (s *jsonStream) releaseLex() {
    for _, piece := range s.pieces {
        piece.field.release()
    }
    // 避免复用的底层数组继续引用字段/事件.
    clear(s.pieces)
    s.pieces = s.pieces[:0]
    s.lexBytes = 0
}

// finish 仅在 EOF/error 时接受未完成但仍合法的前缀.
// 原样回放尾部, 不补造引号, 转义或分隔符.
// done/stop 要求完整文档; 允许 arguments 为空字符串.
// finish 成功或失败均进入终态, 且保持幂等.
func (s *jsonStream) finish(allowIncomplete bool) error {
    if s.finished {
        return s.err
    }
    if s.token == jsonStreamNumeric && s.numberComplete() {
        s.token = jsonStreamNoToken
        s.completeValue()
    }
    if !allowIncomplete && s.sawBytes &&
        (s.token != jsonStreamNoToken || len(s.stack) != 0 || !s.rootDone) {
        return s.fail(ErrInvalidJSON)
    }
    if s.restorer != nil {
        // 先释放较早的标记候选, 再处理后续原始转义.
        s.restorer.finish()
        s.restorer = nil
    }
    for _, piece := range s.pieces {
        piece.field.write(piece.text)
    }
    s.releaseLex()
    s.dispose()
    return nil
}

func (s *jsonStream) fail(err error) error {
    s.err = err
    // 候选写入不可提交: 调用方丢弃 FIFO.
    // 必须调用 finish, 配对释放 ownedRestorer 保留的引用.
    if s.restorer != nil {
        s.restorer.finish()
        s.restorer = nil
    }
    s.releaseLex()
    s.dispose()
    return err
}

func (s *jsonStream) dispose() {
    s.finished = true
    s.snapshot = nil
    s.stack = nil
    s.pieces = nil
    s.literal = ""
}

func (s *jsonStream) pendingBytes() int {
    pending := s.lexBytes
    if s.restorer != nil {
        pending += s.restorer.pendingBytes()
    }
    return pending
}
