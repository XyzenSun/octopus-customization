package relay

import (
    "errors"
    "net/http"
    "testing"

    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/privacy"
    "github.com/gin-gonic/gin"
    "github.com/looplj/axonhub/llm"
)

// 原生 HTTP 错误仍可能提供真实 usage. 通过正式入口确认计费与最终渠道归因.
func privacyRelayHTTPErrorUsage(t *testing.T) {
    t.Run("DirectHTTPError", func(t *testing.T) {
        privacyRelayReset(t)
        upstream := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        upstream.status = http.StatusTooManyRequests
        upstream.body = []byte(`{"error":{"type":"rate_limit_error","message":"synthetic error"},"usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}`)
        harness := privacyRelayNewHarness(t, upstream, true, nil)
        status, _ := harness.exchange("/v1/chat/completions", privacyRelayRequest(upstream.format, harness.requestModel(true), "ordinary", false, false), harness.key.APIKey, "application/json")
        if status != http.StatusTooManyRequests || upstream.calls() != 1 {
            t.Fatalf("direct error: status=%d, calls=%d", status, upstream.calls())
        }
        harness.usage(17, 5)
        row := harness.log(false)
        if row.Error == "" || row.ChannelId != harness.channel.ID {
            t.Fatal("upstream HTTP error was logged as success or assigned another channel")
        }
        channel, err := op.ChannelGet(harness.channel.ID, t.Context())
        if err != nil || channel.Keys[0].StatusCode != http.StatusTooManyRequests {
            t.Fatal("real HTTP failure lost its Key status")
        }
    })
    t.Run("LaterHTTPErrorIsFinalChannel", func(t *testing.T) {
        privacyRelayReset(t)
        privacyRelaySet(t, model.SettingKeyPassthroughEnabled, "false")
        first := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        first.body = []byte(`{"id":"synthetic_invalid_shape","object":"chat.completion","choices":"invalid choices type","usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}`)
        harness := privacyRelayNewHarness(t, first, false, nil)
        second := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        second.status = http.StatusInternalServerError
        second.body = []byte(`{"error":{"type":"server_error","message":"synthetic final failure"},"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`)
        channel := harness.channel
        channel.ID = 0
        channel.Name += "_final_error"
        channel.Model = "privacy-final-error-model"
        channel.BaseUrls = []model.BaseUrl{{URL: second.server.URL}}
        channel.Keys = []model.ChannelKey{{Enabled: true, ChannelKey: privacyRelayUpstreamKey}}
        if err := op.LLMCreate(model.LLMInfo{Name: channel.Model, LLMPrice: model.LLMPrice{Input: 2, Output: 3}}, t.Context()); err != nil {
            t.Fatal(err)
        }
        if err := op.ChannelCreate(&channel, t.Context()); err != nil {
            t.Fatal(err)
        }
        item := harness.group.Items[0]
        if _, err := op.GroupUpdate(&model.GroupUpdateRequest{
            ID:            harness.group.ID,
            ItemsToUpdate: []model.GroupItemUpdateRequest{{ID: item.ID, Priority: 0}},
            ItemsToAdd:    []model.GroupItemAddRequest{{ChannelID: channel.ID, ModelName: channel.Model, Priority: 1}},
        }, t.Context()); err != nil {
            t.Fatal(err)
        }
        status, _ := harness.exchange("/v1/chat/completions", privacyRelayRequest(first.format, harness.group.Name, "ordinary", false, false), harness.key.APIKey, "application/json")
        if status != http.StatusBadGateway || first.calls() != 1 || second.calls() != 1 {
            t.Fatal("expected two actual attempts and a final upstream failure")
        }
        row := harness.log(false)
        if row.ChannelId != channel.ID || row.ActualModelName != channel.Model || row.Error == "" || row.InputTokens != 24 || row.OutputTokens != 7 {
            t.Fatal("final channel/model or summed actual usage was incorrect")
        }
        for id, want := range map[int][2]int64{harness.channel.ID: {17, 5}, channel.ID: {7, 2}} {
            stats := op.StatsChannelGet(id)
            if stats.InputToken != want[0] || stats.OutputToken != want[1] || stats.RequestFailed != 1 {
                t.Fatalf("actual attempt usage/failure mismatch for channel %d", id)
            }
        }
        stats := op.StatsAPIKeyGet(harness.key.ID)
        after := op.StatsTotalGet()
        if stats.InputToken != 24 || stats.OutputToken != 7 || after.InputToken-harness.totalBefore.InputToken != 24 || after.OutputToken-harness.totalBefore.OutputToken != 7 {
            t.Fatal("actual attempt usage was lost or counted twice")
        }
    })
}

func privacyRelayHeartbeatFIFO(t *testing.T) {
    privacyRelayReset(t)
    upstream := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
    upstream.frames = []privacyRelayFrame{
        {id: "start", data: `{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PH"}}]}`},
        {id: "heartbeat", kind: "heartbeat", data: ""},
        {id: "tail", data: `{"choices":[{"index":0,"delta":{"content":"ONE_TEST]]"},"finish_reason":"stop"}]}`},
        {id: "done", data: "[DONE]"},
    }
    harness := privacyRelayNewHarness(t, upstream, true, nil)
    frames := harness.stream(upstream.format, privacyRelayRequest(upstream.format, harness.requestModel(true), "ordinary", true, false))
    if len(frames) != len(upstream.frames) || frames[1].id != "heartbeat" || frames[1].kind != "heartbeat" || frames[1].data != "" {
        t.Fatal("empty heartbeat was lost or reordered before the privacy FIFO")
    }
    privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[0].data)), "choices.0.delta.content", privacyRelaySource)
    privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[2].data)), "choices.0.delta.content", "")
    harness.log(true)
}

type privacyWriteProbe struct {
    header http.Header
    status int
    limit  int
    err    error
}

func (probe *privacyWriteProbe) Header() http.Header    { return probe.header }
func (probe *privacyWriteProbe) WriteHeader(status int) { probe.status = status }
func (probe *privacyWriteProbe) Write(body []byte) (int, error) {
    return min(probe.limit, len(body)), probe.err
}

// 这里只校准 writer 错误返回, 不代替使用真实 HTTP 的 Mock 集成.
func TestPrivacyWriteBack(t *testing.T) {
    snapshot, err := privacy.Compile(privacy.Config{Enabled: true, Scopes: privacy.Messages, FailurePolicy: privacy.Continue}, []privacy.Rule{
        {Source: privacyRelaySource, Alias: "PHONE_TEST", Enabled: true},
    }, 1)
    if err != nil {
        t.Fatal(err)
    }
    for _, fixture := range []struct {
        name     string
        limit    int
        writeErr error
        success  bool
    }{
        {"ZeroWriteError", 0, errors.New("synthetic writer failure"), false},
        {"ShortWrite", 5, nil, false},
        {"CompleteWrite", 1024, nil, true},
    } {
        t.Run(fixture.name, func(t *testing.T) {
            writer := &privacyWriteProbe{header: make(http.Header), limit: fixture.limit, err: fixture.writeErr}
            context, _ := gin.CreateTestContext(writer)
            metrics := &RelayMetrics{}
            err := writeRelayError(context, snapshot, metrics, http.StatusBadGateway, privacyRelayMarker)
            if fixture.success {
                if err != nil || !metrics.PrivacyTriggered || len(metrics.InternalResponse) == 0 {
                    t.Fatal("successful restored body was not recorded")
                }
            } else if !errors.Is(err, errClientDisconnected) || metrics.PrivacyTriggered || len(metrics.InternalResponse) != fixture.limit {
                t.Fatal("failed write was recorded as a full submitted privacy response")
            }
            if !context.Writer.Written() || writer.status != http.StatusBadGateway {
                t.Fatal("fixture did not reproduce header-only Written() state")
            }
        })
    }
}
