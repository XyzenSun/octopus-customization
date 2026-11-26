package relay

import (
    "io"
    "net/http"
    "net/http/httptest"
    "sync"
    "testing"

    "github.com/gin-gonic/gin"
)

// 校准写回后的 HTTP 中止边界. 完整业务链路另由 TestPrivacyRelayMock 验证.
func TestPrivacyAbortWire(t *testing.T) {
    for _, http2 := range []bool{false, true} {
        name := "HTTP1"
        if http2 {
            name = "HTTP2"
        }
        t.Run(name, func(t *testing.T) {
            gin.SetMode(gin.TestMode)
            allowAbort := make(chan struct{})
            handlerDone := make(chan struct{})
            var release sync.Once
            unblock := func() { release.Do(func() { close(allowAbort) }) }
            router := gin.New()
            router.GET("/stream", func(c *gin.Context) {
                defer close(handlerDone)
                c.Header("Content-Type", "text/event-stream")
                if _, err := c.Writer.Write([]byte("data: committed\n\n")); err != nil {
                    return
                }
                c.Writer.Flush()
                select {
                case <-allowAbort:
                    abortPrivacyStream(c)
                case <-c.Request.Context().Done():
                }
            })
            server := httptest.NewUnstartedServer(router)
            if http2 {
                server.EnableHTTP2 = true
                server.StartTLS()
            } else {
                server.Start()
            }
            t.Cleanup(func() {
                unblock()
                server.Close()
            })
            request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/stream", nil)
            if err != nil {
                t.Fatal(err)
            }
            response, err := server.Client().Do(request)
            if err != nil {
                t.Fatal(err)
            }
            defer response.Body.Close()
            wantMajor := 1
            if http2 {
                wantMajor = 2
            }
            if response.ProtoMajor != wantMajor || response.StatusCode != http.StatusOK {
                t.Fatalf("response protocol/status = %s/%d", response.Proto, response.StatusCode)
            }
            committed := make([]byte, len("data: committed\n\n"))
            if _, err := io.ReadFull(response.Body, committed); err != nil {
                t.Fatal("read committed event:", err)
            }
            unblock()
            tail, err := io.ReadAll(response.Body)
            if err == nil || len(tail) != 0 {
                t.Fatalf("abort must interrupt HTTP with no added bytes: tail=%q, error=%v", tail, err)
            }
            select {
            case <-handlerDone:
            case <-t.Context().Done():
                t.Fatal("abort handler did not finish")
            }
        })
    }
}
