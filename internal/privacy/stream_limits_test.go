package privacy

import (
    "bytes"
    "fmt"
    "strings"
    "testing"

    llm "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
)

func TestPrivacyStreamLimits(t *testing.T) {
    newStream := func(t *testing.T, policy FailurePolicy) *StreamRestorer {
        return NewStreamRestorer(privacyStreamSnapshot(t, Messages, policy), llm.APIFormatOpenAIChatCompletion)
    }
    prefix := &httpclient.StreamEvent{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PH"}}]}`)}
    heartbeat := &httpclient.StreamEvent{Type: "heartbeat"}
    for _, policy := range []FailurePolicy{Continue, Abort} {
        t.Run("pending_events/"+string(policy), func(t *testing.T) {
            stream := newStream(t, policy)
            for index := 0; index < MaxPendingEvents; index++ {
                event := heartbeat
                if index == 0 {
                    event = prefix
                }
                if commits, err := stream.Push(event); len(commits) != 0 || err != nil {
                    t.Fatalf("pending event %d: %d commits, %v", index+1, len(commits), err)
                }
            }
            commits, err := stream.Push(heartbeat)
            if err != ErrResourceLimit {
                t.Fatalf("expected pending event limit, got %v", err)
            }
            if policy == Abort {
                if !stream.Closed() || len(commits) != 0 {
                    t.Fatal("abort must close without returning candidates")
                }
                return
            }
            if stream.Closed() || len(commits) != MaxPendingEvents+1 || !bytes.Equal(commits[0].Event.Data, prefix.Data) {
                t.Fatal("continue must release the complete FIFO in original form")
            }
            for index, commit := range commits {
                if commit.Changed || index > 0 && commit.Event.Type != heartbeat.Type {
                    t.Fatalf("invalid fallback event at %d", index)
                }
            }
        })
    }
    t.Run("pending_bytes", func(t *testing.T) {
        stream := newStream(t, Continue)
        if commits, err := stream.Push(prefix); len(commits) != 0 || err != nil {
            t.Fatalf("prefix: %d commits, %v", len(commits), err)
        }
        // 元数据不会改写, 用同一 FIFO 精确达到包含原事件和未决片段的字节上限.
        extra := &httpclient.StreamEvent{LastEventID: strings.Repeat("a", MaxPendingBytes-stream.pendingBytes())}
        if commits, err := stream.Push(extra); len(commits) != 0 || err != nil || stream.pendingBytes() != MaxPendingBytes {
            t.Fatalf("exact byte limit: %d commits, %v", len(commits), err)
        }
        last := &httpclient.StreamEvent{Type: "x"}
        commits, err := stream.Push(last)
        if err != ErrResourceLimit || len(commits) != 3 || commits[1].Event.LastEventID != extra.LastEventID || commits[2].Event.Type != last.Type {
            t.Fatalf("byte overflow: %d commits, %v", len(commits), err)
        }
    })
    t.Run("logical_streams", func(t *testing.T) {
        stream := newStream(t, Abort)
        event := func(index int, finishing bool) *httpclient.StreamEvent {
            payload := fmt.Sprintf(`{"choices":[{"index":%d,"delta":{"content":"x"}}]}`, index)
            if finishing {
                payload = fmt.Sprintf(`{"choices":[{"index":%d,"finish_reason":"stop","delta":{}}]}`, index)
            }
            return &httpclient.StreamEvent{Data: []byte(payload)}
        }
        for index := 0; index < MaxLogicalStreams; index++ {
            if commits, err := stream.Push(event(index, false)); err != nil || len(commits) != 1 {
                t.Fatalf("active stream %d: %d commits, %v", index+1, len(commits), err)
            }
        }
        if _, err := stream.Push(event(0, true)); err != nil {
            t.Fatalf("retire logical stream: %v", err)
        }
        if _, err := stream.Push(event(MaxLogicalStreams, false)); err != nil || len(stream.streams) != MaxLogicalStreams {
            t.Fatalf("reuse released capacity: %v", err)
        }
        if commits, err := stream.Push(event(MaxLogicalStreams+1, false)); err != ErrResourceLimit || len(commits) != 0 || !stream.Closed() {
            t.Fatalf("active stream overflow: %d commits, %v", len(commits), err)
        }
    })
}

func TestPrivacyStreamCandidateRollback(t *testing.T) {
    stream := NewStreamRestorer(privacyStreamSnapshot(t, Messages, Continue), llm.APIFormatOpenAIChatCompletion)
    events := []*httpclient.StreamEvent{
        {Data: []byte(`{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PH"}}]}`)},
        {Data: []byte(`{"choices":[{"index":0,"delta":{"content":"ONE_1]]","refusal":"[[OCTOPUS_PII:NA"}}]}`)},
        {Data: []byte(`{"choices":[{"delta":{"content":"missing index"}}]}`)},
    }
    for _, event := range events[:2] {
        if commits, err := stream.Push(event); err != nil || len(commits) != 0 {
            t.Fatalf("marker start cannot commit before the consumed tail: %d commits, %v", len(commits), err)
        }
    }
    commits, err := stream.Push(events[2])
    if err != ErrStreamIdentity || len(commits) != len(events) {
        t.Fatalf("continue recovery: %d commits, %v", len(commits), err)
    }
    for index, event := range events {
        if commits[index].Changed || !bytes.Equal(commits[index].Event.Data, event.Data) {
            t.Fatalf("partial rewrite escaped rollback at event %d", index)
        }
    }
    tail := &httpclient.StreamEvent{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PHONE_1]]"}}]}`)}
    commits, err = stream.Push(tail)
    if err != nil || len(commits) != 1 || commits[0].Changed || !bytes.Equal(commits[0].Event.Data, tail.Data) {
        t.Fatal("continue fallback must not restart recovery")
    }
    commits, err = stream.Push(&httpclient.StreamEvent{Data: []byte(`{"choices":`)})
    if err != ErrInvalidJSON || len(commits) != 0 || !stream.Closed() {
        t.Fatal("continue fallback must not emit invalid outer JSON")
    }
}
