package relay

import (
    "encoding/json"
    "errors"
    "net/http"
    "time"

    "github.com/bestruirui/octopus/internal/privacy"
    "github.com/bestruirui/octopus/internal/server/resp"
    "github.com/bestruirui/octopus/internal/utils/log"
    "github.com/gin-gonic/gin"
    "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
)

const privacyFailureMessage = "privacy processing failed"

var errUpstreamStreamFailure = errors.New("upstream stream reported an error")

func isUpstreamStreamFailure(event *httpclient.StreamEvent) bool {
    switch event.Type {
    case "error", "response.failed", "response.error":
        return true
    }
    var envelope struct {
        Type  string                     `json:"type"`
        Error map[string]json.RawMessage `json:"error"`
    }
    if json.Unmarshal(event.Data, &envelope) != nil {
        return false
    }
    return envelope.Error != nil || envelope.Type == "error" || envelope.Type == "response.failed" || envelope.Type == "response.error"
}

func privacyExcludedFormat(format llm.APIFormat) bool {
    switch format {
    case llm.APIFormatOpenAIEmbedding, llm.APIFormatGeminiEmbedding, llm.APIFormatJinaEmbedding,
        llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit, llm.APIFormatOpenAIImageVariation:
        return true
    default:
        return false
    }
}

func isPrivacyFailure(err error) bool {
    var privacyErr *privacy.PrivacyError
    return errors.As(err, &privacyErr)
}

func reportPrivacyFailure(err error) {
    var privacyErr *privacy.PrivacyError
    if errors.As(err, &privacyErr) {
        log.Warnf("privacy processing incomplete: %s", privacyErr.Kind())
    }
}

func restorePrivacyError(snapshot *privacy.Snapshot, body []byte) ([]byte, bool, error) {
    restored, changed, err := snapshot.RestoreError(body)
    if err != nil {
        reportPrivacyFailure(err)
        if snapshot.Config().FailurePolicy == privacy.Continue {
            return body, false, nil
        }
    }
    return restored, changed, err
}

func writeRelayError(c *gin.Context, snapshot *privacy.Snapshot, metrics *RelayMetrics, status int, message string) error {
    body, _ := json.Marshal(resp.ResponseStruct{Code: status, Message: message})
    restored, changed, err := restorePrivacyError(snapshot, body)
    if err != nil {
        status = http.StatusBadGateway
        restored, _ = json.Marshal(resp.ResponseStruct{Code: status, Message: privacyFailureMessage})
        changed = false
    }
    c.Abort()
    written, writeErr := writeRelayBody(c, status, "application/json; charset=utf-8", restored)
    if metrics != nil {
        metrics.InternalResponse = restored[:written]
        metrics.PrivacyTriggered = metrics.PrivacyTriggered || changed && writeErr == nil
    }
    return writeErr
}

// Written() 仅表示状态已写入, 不能证明正文成功提交. 用实际 Write 结果确认日志正文.
func writeRelayBody(c *gin.Context, status int, contentType string, body []byte) (int, error) {
    c.Header("Content-Type", contentType)
    c.Status(status)
    written, err := c.Writer.Write(body)
    if written < 0 || written > len(body) {
        return 0, errClientDisconnected
    }
    if err != nil || written != len(body) {
        c.Abort()
        return written, errClientDisconnected
    }
    return written, nil
}

func relaySnapshot(snapshots []*privacy.Snapshot) *privacy.Snapshot {
    if len(snapshots) == 0 {
        return nil
    }
    return snapshots[0]
}

func (ra *relayAttempt) restorePrivacyResponse(body []byte) ([]byte, bool, error) {
    result, changed, err := ra.privacySnapshot.RestoreResponse(ra.inboundType, body)
    if err != nil {
        reportPrivacyFailure(err)
        if ra.privacySnapshot.Config().FailurePolicy == privacy.Continue {
            return body, false, nil
        }
    }
    return result, changed, err
}

func flushRelayResponse(c *gin.Context) error {
    writer := http.ResponseWriter(c.Writer)
    // Gin Flush 不返回底层错误. 从它包裹的 writer 获取可报告错误的 Flush.
    if wrapped, ok := writer.(interface{ Unwrap() http.ResponseWriter }); ok {
        writer = wrapped.Unwrap()
    }
    return http.NewResponseController(writer).Flush()
}

// 在写回后触发 abort 时停止 HTTP 响应的收尾写入. net/http 会关闭 HTTP/1
// 连接或重置 HTTP/2 stream, 客户端可观察到断流, 不会收到伪造的成功终止.
func abortPrivacyStream(c *gin.Context) {
    c.Abort()
    if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now()); err != nil {
        log.Warnf("failed to close privacy-aborted response")
    }
}

func deleteContentLength(headers http.Header) {
    headers.Del("Content-Length")
}
