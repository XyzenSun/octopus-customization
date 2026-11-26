package privacy

import (
    "fmt"
    "runtime"
    "strings"
    "sync"
    "testing"
)

const privacyBenchmarkVersion uint64 = 1

// 仅消费数值结果, 不把原文、规则或快照保留在全局或写入基准日志.
var privacyBenchmarkSink uint64

var privacyBenchmarkTextSizes = [...]struct {
    name  string
    bytes int
}{
    {name: "1KiB", bytes: 1024},
    {name: "64KiB", bytes: 64 * 1024},
}

type privacyBenchmarkFixture struct {
    input   string
    want    string
    changed bool
}

type privacyBenchmarkResult struct {
    requests uint64
    bytes    uint64
    changes  uint64
    failures uint64
}

func privacyBenchmarkRules(count int) []Rule {
    rules := make([]Rule, count)
    for index := range rules {
        // 全部启用, 原文 18 bytes、别名 10 bytes, 固定宽度保证唯一且无包含关系.
        rules[index] = Rule{
            ID:      index + 1,
            Source:  fmt.Sprintf("BENCH_SOURCE_%05d", index),
            Alias:   fmt.Sprintf("RULE_%05d", index),
            Enabled: true,
        }
    }
    return rules
}

func privacyBenchmarkText(ruleCount, textBytes int, repeated, restore bool) privacyBenchmarkFixture {
    if !repeated {
        text := strings.Repeat(".", textBytes)
        return privacyBenchmarkFixture{input: text, want: text}
    }

    // 只重复最后一条规则. oracle 按固定格式独立构造, 不调用被测处理器或读取快照映射.
    source := fmt.Sprintf("BENCH_SOURCE_%05d", ruleCount-1)
    marker := fmt.Sprintf("[[OCTOPUS_PII:RULE_%05d]]", ruleCount-1)
    inputToken, outputToken := source, marker
    if restore {
        inputToken, outputToken = marker, source
    }
    // 每个输入单元恰好 64 bytes, 含一个命中、一个分隔符及纯字面填充.
    padding := strings.Repeat(".", 64-len(inputToken)-1)
    return privacyBenchmarkFixture{
        input:   strings.Repeat(inputToken+"|"+padding, textBytes/64),
        want:    strings.Repeat(outputToken+"|"+padding, textBytes/64),
        changed: true,
    }
}

func privacyBenchmarkSnapshot(b *testing.B, rules []Rule) *Snapshot {
    b.Helper()
    config := Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}
    snapshot, err := Compile(config, rules, privacyBenchmarkVersion)
    if err != nil || snapshot == nil {
        b.Fatal("基准快照编译失败")
    }
    // 保留真实固定版本 DFA, 不安装 stub, 不修改已编译快照的任何容器.
    if !snapshot.Active() || snapshot.Config() != config || snapshot.Version() != privacyBenchmarkVersion ||
        snapshot.RuleCount() != len(rules) || snapshot.matcher == nil || snapshot.matcher.PatternCount() != len(rules) ||
        len(snapshot.patterns) != len(rules) || len(snapshot.markers) != len(rules) ||
        len(snapshot.restoreMap) != len(rules) || snapshot.maxMarkerLength != 26 {
        b.Fatal("基准快照结构校验失败")
    }
    for index, rule := range rules {
        stored, ok := snapshot.Rule(index)
        marker := "[[OCTOPUS_PII:" + rule.Alias + "]]"
        if !ok || stored != rule || snapshot.patterns[index] != rule.Source ||
            snapshot.markers[index] != marker || snapshot.restoreMap[marker] != rule.Source {
            b.Fatal("基准快照映射校验失败")
        }
    }
    return snapshot
}

func privacyBenchmarkValidateReplace(b *testing.B, snapshot *Snapshot, fixture privacyBenchmarkFixture) {
    b.Helper()
    output, changed, err := snapshot.Replace(fixture.input)
    if err != nil || output != fixture.want || changed != fixture.changed {
        b.Fatal("替换基准独立期望校验失败")
    }
}

func privacyBenchmarkDeltas(input string, deltaBytes int) []string {
    parts := make([]string, 0, (len(input)+deltaBytes-1)/deltaBytes)
    for start := 0; start < len(input); start += deltaBytes {
        end := start + deltaBytes
        if end > len(input) {
            end = len(input)
        }
        parts = append(parts, input[start:end])
    }
    return parts
}

func privacyBenchmarkValidateRestore(b *testing.B, snapshot *Snapshot, fixture privacyBenchmarkFixture, parts []string) {
    b.Helper()
    restorer := NewRestorer(snapshot)
    var output strings.Builder
    changed := false
    for _, part := range parts {
        text, partChanged := restorer.Push(part)
        output.WriteString(text)
        changed = changed || partChanged
    }
    tail, tailChanged := restorer.Finish()
    output.WriteString(tail)
    changed = changed || tailChanged
    if output.String() != fixture.want || changed != fixture.changed || restorer.HasPending() {
        b.Fatal("恢复基准独立期望校验失败")
    }
}

func BenchmarkPrivacyCompile(b *testing.B) {
    for _, ruleCount := range [...]int{10, 1000, 10000} {
        b.Run(fmt.Sprintf("rules=%d", ruleCount), func(b *testing.B) {
            b.StopTimer()
            rules := privacyBenchmarkRules(ruleCount)
            snapshot := privacyBenchmarkSnapshot(b, rules)
            // 编译的结构及行为验证在计时外完成, oracle 与热请求基准一致且独立.
            for _, repeated := range [...]bool{false, true} {
                privacyBenchmarkValidateReplace(b, snapshot, privacyBenchmarkText(ruleCount, 1024, repeated, false))
                fixture := privacyBenchmarkText(ruleCount, 1024, repeated, true)
                privacyBenchmarkValidateRestore(b, snapshot, fixture, privacyBenchmarkDeltas(fixture.input, 8))
            }
            snapshot = nil
            config := Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}
            var consumed uint64
            b.ReportAllocs()
            b.ResetTimer()
            b.StartTimer()
            for iteration := 0; iteration < b.N; iteration++ {
                compiled, err := Compile(config, rules, privacyBenchmarkVersion)
                if err != nil || compiled == nil {
                    b.Fatal("计时内基准编译失败")
                }
                consumed += uint64(compiled.RuleCount())
                runtime.KeepAlive(compiled)
            }
            b.StopTimer()
            if consumed != uint64(b.N)*uint64(ruleCount) {
                b.Fatal("编译基准结果消费校验失败")
            }
            privacyBenchmarkSink = consumed
        })
    }
}

// 每个 worker 一次性取得 b.N 的商余数分片, 热循环不使用 atomic、channel 或锁派发请求.
// worker 数不乘 GOMAXPROCS, 也不随其改变. 校准时 b.N 小于 worker 数允许空分片, 不缩减 worker 数.
func privacyBenchmarkWorkers(b *testing.B, workers int, run func(int) privacyBenchmarkResult) privacyBenchmarkResult {
    b.Helper()
    b.StopTimer()
    results := make([]privacyBenchmarkResult, workers)
    start := make(chan struct{})
    var ready, done sync.WaitGroup
    ready.Add(workers)
    done.Add(workers)
    for worker := 0; worker < workers; worker++ {
        iterations := b.N / workers
        if worker < b.N%workers {
            iterations++
        }
        go func(worker, iterations int) {
            ready.Done()
            <-start
            // 结果仅在 worker 结束时写入独占槽位, 避免每轮共享计数和伪共享写入.
            results[worker] = run(iterations)
            done.Done()
        }(worker, iterations)
    }
    ready.Wait()
    b.ReportAllocs()
    b.ResetTimer()
    b.StartTimer()
    close(start)
    done.Wait()
    b.StopTimer()

    var total privacyBenchmarkResult
    for _, result := range results {
        total.requests += result.requests
        total.bytes += result.bytes
        total.changes += result.changes
        total.failures += result.failures
    }
    return total
}

func privacyBenchmarkValidateResult(b *testing.B, result privacyBenchmarkResult, fixture privacyBenchmarkFixture) {
    b.Helper()
    changes := uint64(0)
    if fixture.changed {
        changes = uint64(b.N)
    }
    if result.requests != uint64(b.N) || result.bytes != uint64(b.N)*uint64(len(fixture.want)) ||
        result.changes != changes || result.failures != 0 {
        b.Fatal("热请求基准结果消费校验失败")
    }
    privacyBenchmarkSink = result.requests + result.bytes + result.changes
}

func BenchmarkPrivacyReplace(b *testing.B) {
    for _, ruleCount := range [...]int{10, 1000, 10000} {
        b.Run(fmt.Sprintf("rules=%d", ruleCount), func(b *testing.B) {
            b.StopTimer()
            // 本组所有校准和 worker 共享同一不可变快照; 只在组准备时编译一次.
            immutableSnapshot := privacyBenchmarkSnapshot(b, privacyBenchmarkRules(ruleCount))
            for _, size := range privacyBenchmarkTextSizes {
                for _, match := range [...]string{"none", "repeated"} {
                    for _, workers := range [...]int{1, 8, 32} {
                        b.Run(fmt.Sprintf("text=%s/match=%s/workers=%d", size.name, match, workers), func(b *testing.B) {
                            b.StopTimer()
                            fixture := privacyBenchmarkText(ruleCount, size.bytes, match == "repeated", false)
                            if len(fixture.input) != size.bytes {
                                b.Fatal("替换基准输入尺寸校验失败")
                            }
                            privacyBenchmarkValidateReplace(b, immutableSnapshot, fixture)
                            result := privacyBenchmarkWorkers(b, workers, func(iterations int) privacyBenchmarkResult {
                                result := privacyBenchmarkResult{requests: uint64(iterations)}
                                for iteration := 0; iteration < iterations; iteration++ {
                                    output, changed, err := immutableSnapshot.Replace(fixture.input)
                                    result.bytes += uint64(len(output))
                                    if changed {
                                        result.changes++
                                    }
                                    if err != nil {
                                        result.failures++
                                    }
                                    runtime.KeepAlive(output)
                                }
                                return result
                            })
                            privacyBenchmarkValidateResult(b, result, fixture)
                        })
                    }
                }
            }
        })
    }
}

func BenchmarkPrivacyRestore(b *testing.B) {
    for _, ruleCount := range [...]int{10, 1000, 10000} {
        b.Run(fmt.Sprintf("rules=%d", ruleCount), func(b *testing.B) {
            b.StopTimer()
            immutableSnapshot := privacyBenchmarkSnapshot(b, privacyBenchmarkRules(ruleCount))
            for _, size := range privacyBenchmarkTextSizes {
                for _, match := range [...]string{"none", "repeated"} {
                    for _, deltaBytes := range [...]int{1, 8, 64} {
                        for _, workers := range [...]int{1, 8, 32} {
                            b.Run(fmt.Sprintf("text=%s/match=%s/delta=%dbytes/workers=%d", size.name, match, deltaBytes, workers), func(b *testing.B) {
                                b.StopTimer()
                                fixture := privacyBenchmarkText(ruleCount, size.bytes, match == "repeated", true)
                                if len(fixture.input) != size.bytes {
                                    b.Fatal("恢复基准输入尺寸校验失败")
                                }
                                parts := privacyBenchmarkDeltas(fixture.input, deltaBytes)
                                privacyBenchmarkValidateRestore(b, immutableSnapshot, fixture, parts)
                                result := privacyBenchmarkWorkers(b, workers, func(iterations int) privacyBenchmarkResult {
                                    result := privacyBenchmarkResult{requests: uint64(iterations)}
                                    for iteration := 0; iteration < iterations; iteration++ {
                                        // 每次迭代是一份完整逻辑文本, 新恢复器及其 Push/Finish 分配属于热请求成本.
                                        // 计时内逐片消费, 不为测量而额外拼接完整响应; 完整文本校验已在计时外进行.
                                        restorer := NewRestorer(immutableSnapshot)
                                        changed := false
                                        for _, part := range parts {
                                            output, partChanged := restorer.Push(part)
                                            result.bytes += uint64(len(output))
                                            changed = changed || partChanged
                                            runtime.KeepAlive(output)
                                        }
                                        tail, tailChanged := restorer.Finish()
                                        result.bytes += uint64(len(tail))
                                        changed = changed || tailChanged
                                        runtime.KeepAlive(tail)
                                        if changed {
                                            result.changes++
                                        }
                                    }
                                    return result
                                })
                                privacyBenchmarkValidateResult(b, result, fixture)
                            })
                        }
                    }
                }
            }
        })
    }
}
