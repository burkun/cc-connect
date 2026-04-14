package weixin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBodyFromItemList_Text(t *testing.T) {
	got := bodyFromItemList([]messageItem{
		{Type: messageItemText, TextItem: &textItem{Text: "  hello  "}},
	})
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestBodyFromItemList_VoiceText(t *testing.T) {
	got := bodyFromItemList([]messageItem{
		{Type: messageItemVoice, VoiceItem: &voiceItem{Text: "transcribed"}},
	})
	if got != "transcribed" {
		t.Fatalf("got %q", got)
	}
}

func TestBodyFromItemList_Quote(t *testing.T) {
	ref := &refMessage{
		Title: "t",
		MessageItem: &messageItem{
			Type:     messageItemText,
			TextItem: &textItem{Text: "inner"},
		},
	}
	got := bodyFromItemList([]messageItem{
		{Type: messageItemText, TextItem: &textItem{Text: "reply"}, RefMsg: ref},
	})
	want := "[引用: t | inner]\nreply"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSplitUTF8(t *testing.T) {
	s := string([]rune{'a', '啊', 'b', '吧', 'c'})
	parts := splitUTF8(s, 2)
	if len(parts) != 3 || parts[0] != "a啊" || parts[1] != "b吧" || parts[2] != "c" {
		t.Fatalf("parts=%#v", parts)
	}
}

func TestSplitUTF8Empty(t *testing.T) {
	parts := splitUTF8("", maxWeixinChunk)
	if len(parts) != 1 || parts[0] != "" {
		t.Fatalf("parts=%#v", parts)
	}
}

func TestMediaOnlyItems(t *testing.T) {
	if !mediaOnlyItems([]messageItem{{Type: messageItemImage}}) {
		t.Fatal("image should be media-only")
	}
	if mediaOnlyItems([]messageItem{{Type: messageItemVoice, VoiceItem: &voiceItem{Text: "x"}}}) {
		t.Fatal("voice with text is not media-only")
	}
}

func TestSendMessageResp_JSON(t *testing.T) {
	var r sendMessageResp
	if err := json.Unmarshal([]byte(`{"ret":-1,"errcode":100,"errmsg":"rate limited"}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Ret != -1 || r.Errcode != 100 || r.Errmsg != "rate limited" {
		t.Fatalf("got %+v", r)
	}
}

func TestSendAudioRejectsEmptyAudio(t *testing.T) {
	p := &Platform{}
	// resolveReplyContext checks context_token first, so provide one
	rc := &replyContext{peerUserID: "test", contextToken: "valid-token"}
	err := p.SendAudio(context.Background(), rc, []byte{}, "wav")
	if err == nil {
		t.Fatal("expected error for empty audio")
	}
	if !containsStr(err.Error(), "empty audio") {
		t.Fatalf("expected 'empty audio' error, got: %v", err)
	}
}

func TestSendAudioRejectsInvalidReplyContext(t *testing.T) {
	p := &Platform{}
	err := p.SendAudio(context.Background(), "invalid-context", []byte("audio-data"), "wav")
	if err == nil {
		t.Fatal("expected error for invalid reply context")
	}
	if !containsStr(err.Error(), "invalid reply context") {
		t.Fatalf("expected 'invalid reply context' error, got: %v", err)
	}
}

func TestSendAudioRejectsNilReplyContext(t *testing.T) {
	p := &Platform{}
	err := p.SendAudio(context.Background(), nil, []byte("audio-data"), "wav")
	if err == nil {
		t.Fatal("expected error for nil reply context")
	}
	if !containsStr(err.Error(), "invalid reply context") {
		t.Fatalf("expected 'invalid reply context' error, got: %v", err)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStrHelper(s, substr))
}

func containsStrHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestReplyContextHasGeneratingClientID(t *testing.T) {
	rc := &replyContext{
		peerUserID:         "user1",
		contextToken:       "tok",
		generatingClientID: "cc-gen-abc123",
	}
	if rc.generatingClientID == "" {
		t.Fatal("generatingClientID should not be empty")
	}
	if !containsStr(rc.generatingClientID, "cc-gen-") {
		t.Fatalf("generatingClientID should have cc-gen- prefix, got %q", rc.generatingClientID)
	}
}

func TestSendGeneratingHeartbeatBuildsCorrectMessage(t *testing.T) {
	// Verify the message structure built by sendGeneratingHeartbeat
	msg := sendMessageReq{
		Msg: weixinOutboundMsg{
			FromUserID:   "",
			ToUserID:     "user1",
			ClientID:     "cc-gen-test",
			MessageType:  messageTypeBot,
			MessageState: messageStateGenerating,
			ContextToken: "test-token",
		},
	}
	if msg.Msg.MessageState != messageStateGenerating {
		t.Fatalf("expected messageState=%d, got %d", messageStateGenerating, msg.Msg.MessageState)
	}
	if msg.Msg.MessageType != messageTypeBot {
		t.Fatalf("expected messageType=%d, got %d", messageTypeBot, msg.Msg.MessageType)
	}
	if msg.Msg.ItemList != nil {
		t.Fatalf("expected nil ItemList for GENERATING heartbeat, got %v", msg.Msg.ItemList)
	}
	if msg.Msg.ClientID != "cc-gen-test" {
		t.Fatalf("expected ClientID=cc-gen-test, got %q", msg.Msg.ClientID)
	}
	if msg.Msg.ContextToken != "test-token" {
		t.Fatalf("expected ContextToken=test-token, got %q", msg.Msg.ContextToken)
	}
}

func TestMessageStateConstants(t *testing.T) {
	if messageStateGenerating != 1 {
		t.Fatalf("expected messageStateGenerating=1, got %d", messageStateGenerating)
	}
	if messageStateFinish != 2 {
		t.Fatalf("expected messageStateFinish=2, got %d", messageStateFinish)
	}
}
