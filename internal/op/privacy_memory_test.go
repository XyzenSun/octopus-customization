package op

import (
    "fmt"
    "runtime"
    "testing"
    "weak"

    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/privacy"
)

const (
    privacyMemoryRuleCount    = 1000
    privacyMemoryRefreshCount = 100
    privacyMemoryBatchSize    = 10
)

type privacyMemorySample struct {
    round       int
    heapAlloc   uint64
    heapObjects uint64
}

type privacyMemoryReport struct {
    baseline        privacyMemorySample
    samples         [privacyMemoryRefreshCount / privacyMemoryBatchSize]privacyMemorySample
    publicationPeak privacyMemorySample
    held            privacyMemorySample
}

// 复用已编译二进制的独立 worker, 不重新运行 go test 或初始化生产数据目录.
// 这里只观察纯 Go GC 可达性, 不等价于 race 验证或向 OS 归还内存的验证.
func TestPrivacySnapshotReclaim(t *testing.T) {
    if !*octopusPrivacyWorker {
        privacyTestRunWorker(t, "TestPrivacySnapshotReclaim", "reclaim", "")
        return
    }
    if *privacyTestPhase != "reclaim" {
        t.Fatal("unknown snapshot reclaim worker phase")
    }

    // helper 在本 worker 的 t.TempDir 中建 SQLite, InitDB/InitCache 各一次.
    privacyTestInit(t, "")
    privacyMemorySeed(t)
    runtime.GC()
    baseline := privacyMemoryRead(0)
    report, heldWeak := privacyMemoryPublishRounds(t, baseline)

    // 发布函数已返回, 最后十个旧快照的强引用不再留在其活动栈帧中.
    // 当前快照仍由正式 atomic.Pointer 持有, 作为弱引用存活的对照.
    currentWeak := weak.Make(PrivacySnapshot())
    privacyMemoryRequireCollected(t, privacyMemoryRefreshCount, heldWeak[:])
    report.samples[len(report.samples)-1] = privacyMemoryRead(privacyMemoryRefreshCount)
    if currentWeak.Value() == nil || currentWeak.Value() != PrivacySnapshot() {
        t.Fatal("GC lost or replaced the formally published current snapshot")
    }
    privacyMemoryCheckSnapshot(t, PrivacySnapshot())
    privacyMemoryLog(t, report)
    privacyTestWorkerOK(t, "TestPrivacySnapshotReclaim")
}

// 一次正式导入只编译、发布一个完整候选, 不用一千轮 Create 产生 O(N²) 编译.
// 返回后不让备份夹具或查询结果继续影响后续存活堆采样.
func privacyMemorySeed(t *testing.T) {
    t.Helper()
    dump := &model.DBDump{
        Version: dbDumpVersion,
        Settings: []model.Setting{
            {Key: model.SettingKeyPrivacyEnabled, Value: "true"},
            {Key: model.SettingKeyPrivacyScopes, Value: "1"},
            {Key: model.SettingKeyPrivacyFailurePolicy, Value: "continue"},
        },
        PrivacyRules: make([]model.PrivacyRule, privacyMemoryRuleCount),
    }
    for index := range dump.PrivacyRules {
        dump.PrivacyRules[index] = model.PrivacyRule{
            ID:      index + 1,
            Source:  model.NewPrivacyBytes(fmt.Sprintf("privacy_reclaim_source_%04d", index)),
            Alias:   model.NewPrivacyBytes(fmt.Sprintf("RECLAIM_%04d", index)),
            Enabled: true,
        }
    }
    result, err := DBImportIncremental(t.Context(), dump)
    if err != nil {
        t.Fatal("formal reclaim fixture import:", err)
    }
    if result == nil || result.RowsAffected["privacy_rules"] != privacyMemoryRuleCount {
        t.Fatal("formal import did not insert exactly 1000 privacy rules")
    }
    rows, err := PrivacyRuleList(t.Context())
    if err != nil || len(rows) != privacyMemoryRuleCount {
        t.Fatal("formal list did not read exactly 1000 persisted privacy rules")
    }
    for index, row := range rows {
        expected := dump.PrivacyRules[index]
        if row.ID != expected.ID || row.Source != expected.Source || row.Alias != expected.Alias || !row.Enabled {
            t.Fatal("persisted reclaim fixture differs from the enabled import")
        }
    }
    privacyMemoryCheckSnapshot(t, PrivacySnapshot())
}

// 保持此所有权边界独立: 返回值只有数值和弱引用, 不返回旧 Snapshot.
// 最后一批额外强引用离开此函数后再做回收断言, 避免测试自身延长其生命周期.
//
//go:noinline
func privacyMemoryPublishRounds(t *testing.T, baseline privacyMemorySample) (privacyMemoryReport, [privacyMemoryBatchSize]weak.Pointer[privacy.Snapshot]) {
    t.Helper()
    report := privacyMemoryReport{baseline: baseline, publicationPeak: baseline}
    for batch := 0; batch < len(report.samples); batch++ {
        requests, handles := privacyMemoryRefreshBatch(t, batch, &report.publicationPeak)
        round := (batch + 1) * privacyMemoryBatchSize
        if round == privacyMemoryRefreshCount {
            // 模拟十个仍持有旧快照的请求, 释放普通请求容器后故意保留它们.
            held := requests
            clear(requests[:])
            runtime.GC()
            report.held = privacyMemoryRead(round)
            for index, pointer := range handles {
                if held[index] == nil || pointer.Value() != held[index] {
                    t.Fatalf("held old snapshot %d disappeared while strongly referenced", index)
                }
            }
            // 按 weak.Pointer 文档, 放在 GC 和 Value 检查之后保证强引用存活.
            runtime.KeepAlive(held)
            clear(held[:])
            return report, handles
        }

        // 每十次明确释放全部旧请求引用, GC 后不仅记录堆, 还验证弱引用变空.
        clear(requests[:])
        privacyMemoryRequireCollected(t, round, handles[:])
        report.samples[batch] = privacyMemoryRead(round)
    }
    t.Fatal("reclaim suite did not reach its fixed final publication batch")
    return report, [privacyMemoryBatchSize]weak.Pointer[privacy.Snapshot]{}
}

// 批内持有十个不同版本, 模拟请求各自固定快照; 不导入第二次或直接 Compile.
// 不内联以确保 old/current 临时强引用的栈帧在批后 GC 前已退出.
//
//go:noinline
func privacyMemoryRefreshBatch(t *testing.T, batch int, peak *privacyMemorySample) ([privacyMemoryBatchSize]*privacy.Snapshot, [privacyMemoryBatchSize]weak.Pointer[privacy.Snapshot]) {
    t.Helper()
    var requests [privacyMemoryBatchSize]*privacy.Snapshot
    var handles [privacyMemoryBatchSize]weak.Pointer[privacy.Snapshot]
    for index := range requests {
        old := PrivacySnapshot()
        if old == nil {
            t.Fatal("missing snapshot before formal refresh")
        }
        requests[index], handles[index] = old, weak.Make(old)
        if err := PrivacyRefresh(t.Context()); err != nil {
            t.Fatal("formal snapshot reclaim refresh:", err)
        }
        current := PrivacySnapshot()
        if current == old || current.Version() != old.Version()+1 {
            t.Fatal("formal refresh did not publish a distinct consecutive snapshot version")
        }
        privacyMemoryCheckSnapshot(t, current)
        sample := privacyMemoryRead(batch*privacyMemoryBatchSize + index + 1)
        privacyMemoryAccumulatePeak(peak, sample)
    }
    runtime.KeepAlive(requests)
    return requests, handles
}

func privacyMemoryCheckSnapshot(t *testing.T, snapshot *privacy.Snapshot) {
    t.Helper()
    expectedConfig := privacy.Config{Enabled: true, Scopes: privacy.Messages, FailurePolicy: privacy.Continue}
    if snapshot == nil || snapshot.RuleCount() != privacyMemoryRuleCount || snapshot.Config() != expectedConfig || !snapshot.Active() {
        t.Fatal("formal snapshot did not publish exactly 1000 active rules with the imported configuration")
    }
    for index := 0; index < snapshot.RuleCount(); index++ {
        rule, ok := snapshot.Rule(index)
        if !ok || rule.ID != index+1 || !rule.Enabled {
            t.Fatal("formal refresh changed rule IDs or left a disabled rule")
        }
    }
    // 手写首尾规则的期望, 验证正式 matcher 和反查表确实随快照存在.
    const source = "privacy_reclaim_source_0000 | privacy_reclaim_source_0999"
    const markers = "[[OCTOPUS_PII:RECLAIM_0000]] | [[OCTOPUS_PII:RECLAIM_0999]]"
    privacyTestAssertText(t, snapshot, source, markers, true)
    privacyTestAssertRestore(t, snapshot, markers, source)
}

func privacyMemoryRequireCollected(t *testing.T, round int, handles []weak.Pointer[privacy.Snapshot]) {
    t.Helper()
    // weak.Value 不承诺所有对象最终变 nil; tiny 无指针对象可能共享分配槽.
    // 本例 Snapshot 含多个指针且非 tiny. 十轮是有界 GC 观察, 不是性能阈值.
    // 不用 finalizer、unsafe 或生产 reset/Close 来人为制造回收成功.
    const gcAttempts = 10
    live := len(handles)
    for attempt := 0; attempt < gcAttempts; attempt++ {
        if err := t.Context().Err(); err != nil {
            t.Fatal("snapshot reclaim worker canceled:", err)
        }
        runtime.GC()
        runtime.Gosched()
        live = privacyMemoryLiveCount(handles)
        if live == 0 {
            return
        }
    }
    t.Fatalf("after publication %d and %d GC observations, %d/%d released old snapshots still have non-nil weak pointers",
        round, gcAttempts, live, len(handles))
}

// Value 会临时取得强引用; 独立且不内联的栈帧只返回计数, 不把它带入下一轮 GC.
//
//go:noinline
func privacyMemoryLiveCount(handles []weak.Pointer[privacy.Snapshot]) int {
    live := 0
    for _, pointer := range handles {
        if pointer.Value() != nil {
            live++
        }
    }
    return live
}

func privacyMemoryRead(round int) privacyMemorySample {
    var stats runtime.MemStats
    runtime.ReadMemStats(&stats)
    return privacyMemorySample{round: round, heapAlloc: stats.HeapAlloc, heapObjects: stats.HeapObjects}
}

// 两个指标分别取采样最大值, 不声称捕获编译内部瞬时峰值或整个进程峰值.
func privacyMemoryAccumulatePeak(peak *privacyMemorySample, sample privacyMemorySample) {
    if sample.heapAlloc > peak.heapAlloc {
        peak.heapAlloc = sample.heapAlloc
    }
    if sample.heapObjects > peak.heapObjects {
        peak.heapObjects = sample.heapObjects
    }
}

func privacyMemoryLog(t *testing.T, report privacyMemoryReport) {
    t.Helper()
    // 全部采样结束后才写日志, 避免 testing 保留的日志分配干扰尾段趋势.
    t.Logf("pure-Go GC observation: refreshes=%d rules=%d batch=%d; all 100 released old snapshots verified weak=nil; held-10 verified alive before release; current snapshot remains published",
        privacyMemoryRefreshCount, privacyMemoryRuleCount, privacyMemoryBatchSize)
    t.Logf("baseline after import/GC: HeapAlloc=%d bytes HeapObjects=%d", report.baseline.heapAlloc, report.baseline.heapObjects)
    t.Logf("publication-end observed peaks (not instantaneous peaks): HeapAlloc=%d bytes HeapObjects=%d",
        report.publicationPeak.heapAlloc, report.publicationPeak.heapObjects)
    t.Logf("round=100 with 10 old snapshots strongly held after GC: HeapAlloc=%d bytes HeapObjects=%d",
        report.held.heapAlloc, report.held.heapObjects)
    var gcPeak privacyMemorySample
    for _, sample := range report.samples {
        privacyMemoryAccumulatePeak(&gcPeak, sample)
        t.Logf("round=%d after releasing old requests and GC: HeapAlloc=%d bytes HeapObjects=%d",
            sample.round, sample.heapAlloc, sample.heapObjects)
    }
    t.Logf("post-GC sampled peaks: HeapAlloc=%d bytes HeapObjects=%d", gcPeak.heapAlloc, gcPeak.heapObjects)
    released := report.samples[len(report.samples)-1]
    t.Logf("held-10 release delta: HeapAlloc=%+d bytes HeapObjects=%+d",
        int64(released.heapAlloc)-int64(report.held.heapAlloc), int64(released.heapObjects)-int64(report.held.heapObjects))

    // 固定最后五个无旧强引用的 GC 样本, 给出端点差值及线性斜率, 不判定好坏.
    tail := report.samples[len(report.samples)-5:]
    first, last := tail[0], tail[len(tail)-1]
    var allocSum, objectsSum, xSum float64
    for _, sample := range tail {
        xSum += float64(sample.round)
        allocSum += float64(sample.heapAlloc)
        objectsSum += float64(sample.heapObjects)
    }
    n := float64(len(tail))
    xMean, allocMean, objectsMean := xSum/n, allocSum/n, objectsSum/n
    var xVariance, allocCovariance, objectsCovariance float64
    for _, sample := range tail {
        dx := float64(sample.round) - xMean
        xVariance += dx * dx
        allocCovariance += dx * (float64(sample.heapAlloc) - allocMean)
        objectsCovariance += dx * (float64(sample.heapObjects) - objectsMean)
    }
    t.Logf("post-GC tail rounds=%d..%d samples=%d: HeapAlloc delta=%+d bytes slope=%+.2f bytes/refresh; HeapObjects delta=%+d slope=%+.2f objects/refresh",
        first.round, last.round, len(tail), int64(last.heapAlloc)-int64(first.heapAlloc), allocCovariance/xVariance,
        int64(last.heapObjects)-int64(first.heapObjects), objectsCovariance/xVariance)
    t.Log("No subjective heap/performance threshold, RSS measurement, race result, or OS-memory-return conclusion; SQLite/runtime/testing allocations are also included in MemStats.")
}
