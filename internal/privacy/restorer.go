package privacy

import "strings"

// LEFT 的 KMP 回退表按实例持有, 不提供可修改的共享数组.
// NORMAL 只保存有效前缀长度, CANDIDATE 复用有界字节缓冲.
// 每个逻辑文本使用独立恢复器, 完成后不重新开启, 也不解析已经还原的输出.
type Restorer struct {
    snapshot     *Snapshot
    prefixTable  [len(MarkerLeft)]int
    prefixLength int
    candidate    []byte
    aliasLength  int
    rightStarted bool
    finished     bool
}

func NewRestorer(snapshot *Snapshot) *Restorer {
    restorer := &Restorer{snapshot: snapshot}
    matched := 0
    for index := 1; index < len(MarkerLeft); index++ {
        for matched > 0 && MarkerLeft[index] != MarkerLeft[matched] {
            matched = restorer.prefixTable[matched-1]
        }
        if MarkerLeft[index] == MarkerLeft[matched] {
            matched++
        }
        restorer.prefixTable[index] = matched
    }
    return restorer
}

func (restorer *Restorer) Push(text string) (string, bool) {
    if restorer.finished || !restorer.snapshot.Active() {
        return text, false
    }
    var output strings.Builder
    changed := false
    literal := func(text string) { output.WriteString(text) }
    replacement := func(markerLength int, source string) {
        output.WriteString(source)
        changed = true
    }
    for index := 0; index < len(text); index++ {
        restorer.feed(text[index], literal, replacement)
    }
    return output.String(), changed
}

// 输出回调可直接拼接文本, 也可把字面和还原结果写回所属 SSE 字段.
func (restorer *Restorer) feed(current byte, literal func(string), replacement func(int, string)) {
    if len(restorer.candidate) == 0 {
        restorer.pushNormalByte(current, literal)
        return
    }
    restorer.pushCandidateByte(current, literal, replacement)
}

// 前缀回退只释放已经不可能属于 LEFT 的字节. 当前字节仍只进入一次前缀匹配.
func (restorer *Restorer) pushNormalByte(current byte, literal func(string)) {
    for restorer.prefixLength > 0 && MarkerLeft[restorer.prefixLength] != current {
        fallbackLength := restorer.prefixTable[restorer.prefixLength-1]
        literal(MarkerLeft[:restorer.prefixLength-fallbackLength])
        restorer.prefixLength = fallbackLength
    }
    if MarkerLeft[restorer.prefixLength] != current {
        var single = [1]byte{current}
        literal(string(single[:]))
        return
    }
    restorer.prefixLength++
    if restorer.prefixLength == len(MarkerLeft) {
        restorer.candidate = append(restorer.candidate[:0], MarkerLeft...)
        restorer.prefixLength = 0
        restorer.aliasLength = 0
        restorer.rightStarted = false
    }
}

func (restorer *Restorer) pushCandidateByte(current byte, literal func(string), replacement func(int, string)) {
    if restorer.rightStarted {
        if current != MarkerRight[1] {
            restorer.rejectCandidate(current, literal)
            return
        }
        restorer.candidate = append(restorer.candidate, current)
        // 完整命中先于 L 检查, 最长已知标记正好到达 L 时仍能还原.
        marker := string(restorer.candidate)
        source, exists := restorer.snapshot.restoreMap[marker]
        if exists && source != marker {
            replacement(len(marker), source)
        } else {
            literal(marker)
        }
        restorer.clearCandidate()
        return
    }

    if isAliasByte(current) && restorer.aliasLength < MaxAliasBytes {
        restorer.candidate = append(restorer.candidate, current)
        restorer.aliasLength++
    } else if current == MarkerRight[0] && restorer.aliasLength > 0 {
        restorer.candidate = append(restorer.candidate, current)
        restorer.rightStarted = true
    } else {
        restorer.rejectCandidate(current, literal)
        return
    }

    // 未完整的候选达到 L 后不可能命中本快照. 字面释放而不缓存到固定格式最大值.
    if len(restorer.candidate) >= restorer.snapshot.maxMarkerLength {
        literal(string(restorer.candidate))
        restorer.clearCandidate()
    }
}

func (restorer *Restorer) rejectCandidate(current byte, literal func(string)) {
    // 候选内只有 LEFT、ASCII 别名和至多一个右括号, 不含另一个 LEFT 的前缀后缀.
    // 释放原候选后复用当前字节的 NORMAL 处理, 支持紧邻或重叠的新 LEFT.
    literal(string(restorer.candidate))
    restorer.clearCandidate()
    restorer.pushNormalByte(current, literal)
}

func (restorer *Restorer) clearCandidate() {
    restorer.candidate = restorer.candidate[:0]
    restorer.aliasLength = 0
    restorer.rightStarted = false
}

func (restorer *Restorer) HasPending() bool {
    return restorer.prefixLength > 0 || len(restorer.candidate) > 0
}

// 非 ASCII rune 不可能继续任何候选或前缀. 释放字面尾部后可整 rune 透过.
func (restorer *Restorer) flushLiteral(literal func(string)) {
    if restorer.prefixLength > 0 {
        literal(MarkerLeft[:restorer.prefixLength])
        restorer.prefixLength = 0
    }
    if len(restorer.candidate) > 0 {
        literal(string(restorer.candidate))
        restorer.clearCandidate()
    }
}

// EOF 只释放原始字面尾部, 不补全标记. Finish 幂等且释放对快照和候选缓冲的引用.
func (restorer *Restorer) Finish() (string, bool) {
    if restorer.finished {
        return "", false
    }
    restorer.finished = true
    var output strings.Builder
    restorer.flushLiteral(func(text string) { output.WriteString(text) })
    restorer.candidate = nil
    restorer.snapshot = nil
    return output.String(), false
}
