package storage

import (
	"context"
	"testing"

	"encoding/hex"

	"github.com/keybase/client/go/externals"
	"github.com/keybase/client/go/kbtest"
	"github.com/keybase/client/go/libkb"
	"github.com/keybase/client/go/protocol/chat1"
	"github.com/keybase/client/go/protocol/gregor1"
	"github.com/stretchr/testify/require"
)

func setupInboxTest(t testing.TB, name string) (libkb.TestContext, *Inbox, gregor1.UID) {
	tc := externals.SetupTest(t, name, 2)
	u, err := kbtest.CreateAndSignupFakeUser("ib", tc.G)
	require.NoError(t, err)
	f := func() libkb.SecretUI {
		return &libkb.TestSecretUI{Passphrase: u.Passphrase}
	}
	uid := gregor1.UID(u.User.GetUID().ToBytes())
	return tc, NewInbox(tc.G, uid, f), uid
}

func makeTlfID() chat1.TLFID {
	return randBytes(8)
}

func makeConvo(mtime gregor1.Time, rmsg chat1.MessageID, mmsg chat1.MessageID) chat1.Conversation {
	return chat1.Conversation{
		Metadata: chat1.ConversationMetadata{
			ConversationID: randBytes(8),
			IdTriple: chat1.ConversationIDTriple{
				Tlfid:     makeTlfID(),
				TopicType: chat1.TopicType_CHAT,
				TopicID:   randBytes(8),
			},
			Visibility: chat1.TLFVisibility_PRIVATE,
			Status:     chat1.ConversationStatus_UNFILED,
		},
		ReaderInfo: &chat1.ConversationReaderInfo{
			Mtime:     mtime,
			ReadMsgid: rmsg,
			MaxMsgid:  mmsg,
		},
	}
}

func makeInboxMsg(id chat1.MessageID, typ chat1.MessageType) chat1.MessageBoxed {
	return chat1.MessageBoxed{
		ClientHeader: chat1.MessageClientHeader{
			MessageType: typ,
		},
		ServerHeader: &chat1.MessageServerHeader{
			MessageID: id,
		},
	}
}

func convListCompare(t *testing.T, l []chat1.Conversation, r []chat1.Conversation, name string) {
	require.Equal(t, len(l), len(r), name+" size mismatch")
	for i := 0; i < len(l); i++ {
		t.Logf("convListCompare: l: %s(%d) r: %s(%d)", l[i].GetConvID(), l[i].GetMtime(),
			r[i].GetConvID(), r[i].GetMtime())
		require.Equal(t, l[i], r[i], name+" mismatch")
	}
}

func TestInboxBasic(t *testing.T) {

	_, inbox, _ := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}

	// Fetch with no query parameter
	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	vers, res, _, err := inbox.Read(context.TODO(), nil, nil)

	require.NoError(t, err)
	require.Equal(t, chat1.InboxVers(1), vers, "version mismatch")
	convListCompare(t, convs, res, "basic")
	require.Equal(t, gregor1.Time(numConvs-1), res[0].GetMtime(), "order wrong")

	// Fetch half of the messages (expect miss on first try)
	vers, res, _, err = inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num: numConvs / 2,
	})
	require.IsType(t, MissError{}, err, "expected miss error")
	require.NoError(t, inbox.Merge(context.TODO(), 2, convs, nil, &chat1.Pagination{
		Num: numConvs / 2,
	}))
	vers, res, _, err = inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num: numConvs / 2,
	})
	require.NoError(t, err)
	require.Equal(t, chat1.InboxVers(2), vers, "version mismatch")
	convListCompare(t, convs[:numConvs/2], res, "half")
}

func TestInboxQueries(t *testing.T) {

	_, inbox, _ := setupInboxTest(t, "queries")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 20
	var convs []chat1.Conversation
	for i := 0; i < numConvs; i++ {
		conv := makeConvo(gregor1.Time(i), 1, 1)
		convs = append(convs, conv)
	}

	// Make two dev convos
	var devs, publics, unreads, ignored, full []chat1.Conversation
	convs[3].Metadata.IdTriple.TopicType = chat1.TopicType_DEV
	convs[7].Metadata.IdTriple.TopicType = chat1.TopicType_DEV
	devs = append(devs, []chat1.Conversation{convs[7], convs[3]}...)

	// Make one public convos
	convs[13].Metadata.Visibility = chat1.TLFVisibility_PUBLIC
	publics = append(publics, convs[13])

	// Make three unread convos
	makeUnread := func(ri *chat1.ConversationReaderInfo) {
		ri.MaxMsgid = 5
		ri.ReadMsgid = 3
	}
	makeUnread(convs[5].ReaderInfo)
	makeUnread(convs[13].ReaderInfo)
	makeUnread(convs[19].ReaderInfo)
	unreads = append(unreads, []chat1.Conversation{convs[19], convs[13], convs[5]}...)

	// Make two ignored
	convs[18].Metadata.Status = chat1.ConversationStatus_IGNORED
	convs[4].Metadata.Status = chat1.ConversationStatus_IGNORED
	ignored = append(ignored, []chat1.Conversation{convs[18], convs[4]}...)

	// Mark one as finalized and superseded by
	convs[6].Metadata.FinalizeInfo = &chat1.ConversationFinalizeInfo{
		ResetFull: "reset",
	}
	convs[6].SupersededBy = append(convs[6].SupersededBy, convs[17].Metadata)
	convs[17].Supersedes = append(convs[17].Supersedes, convs[6].Metadata)
	for i := len(convs) - 1; i >= 0; i-- {
		if i == 6 {
			continue
		}
		full = append(full, convs[i])
	}
	for _, conv := range full {
		t.Logf("convID: %s", conv.GetConvID())
	}

	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))

	// Merge in queries and try to read them back out
	var q *chat1.GetInboxQuery
	mergeReadAndCheck := func(t *testing.T, ref []chat1.Conversation, name string) {
		require.NoError(t, inbox.Merge(context.TODO(), 1, []chat1.Conversation{}, q, nil))
		_, res, _, err := inbox.Read(context.TODO(), q, nil)
		require.NoError(t, err)
		convListCompare(t, ref, res, name)
	}
	t.Logf("merging all convs with nil query")
	q = nil
	mergeReadAndCheck(t, full, "all")

	t.Logf("merging dev query")
	devtype := chat1.TopicType_DEV
	q = &chat1.GetInboxQuery{TopicType: &devtype}
	mergeReadAndCheck(t, devs, "devs")

	t.Logf("merging public query")
	publicVis := chat1.TLFVisibility_PUBLIC
	q = &chat1.GetInboxQuery{TlfVisibility: &publicVis}
	mergeReadAndCheck(t, publics, "public")

	t.Logf("merging unread query")
	q = &chat1.GetInboxQuery{UnreadOnly: true}
	mergeReadAndCheck(t, unreads, "unread")

	t.Logf("merging ignore query")
	q = &chat1.GetInboxQuery{Status: []chat1.ConversationStatus{chat1.ConversationStatus_IGNORED}}
	mergeReadAndCheck(t, ignored, "ignored")

	t.Logf("merging tlf ID query")
	q = &chat1.GetInboxQuery{TlfID: &full[0].Metadata.IdTriple.Tlfid}
	tlfIDs := []chat1.Conversation{full[0]}
	mergeReadAndCheck(t, tlfIDs, "tlfids")

	t.Logf("merging after query")
	after := full[:4]
	atime := gregor1.Time(15)
	q = &chat1.GetInboxQuery{After: &atime}
	mergeReadAndCheck(t, after, "after")

	t.Logf("merging before query")
	before := full[5:]
	btime := gregor1.Time(15)
	q = &chat1.GetInboxQuery{Before: &btime}
	mergeReadAndCheck(t, before, "before")
}

func TestInboxPagination(t *testing.T) {

	_, inbox, _ := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 50
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}
	firstPage := convs[:10]
	secondPage := convs[10:20]
	thirdPage := convs[20:35]

	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))

	// Get first page
	t.Logf("first page")
	require.NoError(t, inbox.Merge(context.TODO(), 1, []chat1.Conversation{}, nil, &chat1.Pagination{
		Num: 10,
	}))
	_, res, p, err := inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 10, p.Num, "wrong pagination number")
	convListCompare(t, firstPage, res, "first page")

	// Get the second page
	t.Logf("second page")
	require.NoError(t, inbox.Merge(context.TODO(), 1, []chat1.Conversation{}, nil, &chat1.Pagination{
		Num:  10,
		Next: p.Next,
	}))
	_, res, p, err = inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num:  10,
		Next: p.Next,
	})
	require.NoError(t, err)
	require.Equal(t, 10, p.Num, "wrong pagination number")
	convListCompare(t, secondPage, res, "second page")

	// Get the third page
	t.Logf("third page")
	require.NoError(t, inbox.Merge(context.TODO(), 1, []chat1.Conversation{}, nil, &chat1.Pagination{
		Num:  15,
		Next: p.Next,
	}))
	_, res, p, err = inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num:  15,
		Next: p.Next,
	})
	require.NoError(t, err)
	require.Equal(t, 15, p.Num, "wrong pagination number")
	convListCompare(t, thirdPage, res, "third page")

	// Get the second page (through prev)
	t.Logf("second page (redux)")
	require.NoError(t, inbox.Merge(context.TODO(), 1, []chat1.Conversation{}, nil, &chat1.Pagination{
		Num:      10,
		Previous: p.Previous,
	}))
	_, res, p, err = inbox.Read(context.TODO(), nil, &chat1.Pagination{
		Num:      10,
		Previous: p.Previous,
	})
	require.NoError(t, err)
	require.Equal(t, 10, p.Num, "wrong pagination number")
	convListCompare(t, secondPage, res, "second page (redux)")

}

func validateBadUpdate(t *testing.T, inbox *Inbox, f func() error) {
	require.IsType(t, VersionMismatchError{}, f())
	_, _, _, err := inbox.Read(context.TODO(), nil, nil)
	require.IsType(t, MissError{}, err)
}

func TestInboxNewConversation(t *testing.T) {
	_, inbox, _ := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}
	convs[5].Metadata.FinalizeInfo = &chat1.ConversationFinalizeInfo{
		ResetFull: "reset",
	}

	t.Logf("basic newconv")
	newConv := makeConvo(gregor1.Time(11), 1, 1)
	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	require.NoError(t, inbox.NewConversation(context.TODO(), 2, newConv))
	_, res, _, err := inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	convs = append([]chat1.Conversation{newConv}, convs...)
	convListCompare(t, convs, res, "newconv")

	t.Logf("repeat conv")
	require.NoError(t, inbox.NewConversation(context.TODO(), 3, newConv))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	convListCompare(t, convs, res, "repeatconv")

	t.Logf("supersede newconv")
	newConv = makeConvo(gregor1.Time(12), 1, 1)
	newConv.Supersedes = append(newConv.Supersedes, convs[6].Metadata)
	require.NoError(t, inbox.NewConversation(context.TODO(), 4, newConv))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	convs = append([]chat1.Conversation{newConv}, convs...)
	convListCompare(t, append(convs[:7], convs[8:]...), res, "newconv finalized")

	validateBadUpdate(t, inbox, func() error {
		return inbox.NewConversation(context.TODO(), 10, newConv)
	})
}

func TestInboxNewMessage(t *testing.T) {

	_, inbox, uid := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}

	uid1 := uid
	uid2, err := hex.DecodeString("22")
	require.NoError(t, err)
	uid3, err := hex.DecodeString("33")
	require.NoError(t, err)

	convs[5].Metadata.ActiveList = []gregor1.UID{uid2, uid3, uid1}
	conv := convs[5]
	msg := makeInboxMsg(2, chat1.MessageType_TEXT)
	msg.ClientHeader.Sender = uid1
	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	_, res, _, err := inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, convs[0].GetConvID(), res[0].GetConvID(), "conv not promoted")
	require.NoError(t, inbox.NewMessage(context.TODO(), 2, conv.GetConvID(), msg))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, conv.GetConvID(), res[0].GetConvID(), "conv not promoted")
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.MaxMsgid, "wrong max msgid")
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.ReadMsgid, "wrong read msgid")
	require.Equal(t, []gregor1.UID{uid1, uid2, uid3}, res[0].Metadata.ActiveList, "active list")
	maxMsg, err := res[0].GetMaxMessage(chat1.MessageType_TEXT)
	require.NoError(t, err)
	require.Equal(t, chat1.MessageID(2), maxMsg.GetMessageID(), "max msg not updated")

	// Send another one from a diff User
	msg = makeInboxMsg(3, chat1.MessageType_TEXT)
	msg.ClientHeader.Sender = uid2
	require.NoError(t, inbox.NewMessage(context.TODO(), 3, conv.GetConvID(), msg))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, chat1.MessageID(3), res[0].ReaderInfo.MaxMsgid, "wrong max msgid")
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.ReadMsgid, "wrong read msgid")
	require.Equal(t, []gregor1.UID{uid2, uid1, uid3}, res[0].Metadata.ActiveList, "active list")
	maxMsg, err = res[0].GetMaxMessage(chat1.MessageType_TEXT)
	require.NoError(t, err)
	require.Equal(t, chat1.MessageID(3), maxMsg.GetMessageID(), "max msg not updated")

	validateBadUpdate(t, inbox, func() error {
		return inbox.NewMessage(context.TODO(), 10, conv.GetConvID(), msg)
	})
}

func TestInboxReadMessage(t *testing.T) {

	_, inbox, _ := setupInboxTest(t, "basic")

	uid2, err := hex.DecodeString("22")
	require.NoError(t, err)

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}

	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	_, res, _, err := inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)

	conv := convs[5]
	msg := makeInboxMsg(2, chat1.MessageType_TEXT)
	msg.ClientHeader.Sender = uid2
	require.NoError(t, inbox.NewMessage(context.TODO(), 2, conv.GetConvID(), msg))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.MaxMsgid, "wrong max msgid")
	require.Equal(t, chat1.MessageID(1), res[0].ReaderInfo.ReadMsgid, "wrong read msgid")
	require.NoError(t, inbox.ReadMessage(context.TODO(), 3, conv.GetConvID(), 2))
	_, res, _, err = inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.MaxMsgid, "wrong max msgid")
	require.Equal(t, chat1.MessageID(2), res[0].ReaderInfo.ReadMsgid, "wrong read msgid")

	validateBadUpdate(t, inbox, func() error {
		return inbox.ReadMessage(context.TODO(), 10, conv.GetConvID(), 3)
	})
}

func TestInboxSetStatus(t *testing.T) {

	_, inbox, uid := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}

	conv := convs[5]
	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	require.NoError(t, inbox.SetStatus(context.TODO(), 2, conv.GetConvID(),
		chat1.ConversationStatus_IGNORED))

	q := chat1.GetInboxQuery{
		Status: []chat1.ConversationStatus{chat1.ConversationStatus_IGNORED},
	}
	require.NoError(t, inbox.Merge(context.TODO(), 2, []chat1.Conversation{}, &q, nil))
	_, res, _, err := inbox.Read(context.TODO(), &q, nil)
	require.NoError(t, err)
	require.Equal(t, 1, len(res), "length")
	require.Equal(t, conv.GetConvID(), res[0].GetConvID(), "id")

	t.Logf("sending new message to wake up conv")
	msg := makeInboxMsg(3, chat1.MessageType_TEXT)
	msg.ClientHeader.Sender = uid
	require.NoError(t, inbox.NewMessage(context.TODO(), 3, conv.GetConvID(), msg))
	_, res, _, err = inbox.Read(context.TODO(), &q, nil)
	require.NoError(t, err)
	require.Equal(t, 0, len(res), "ignore not unset")

	validateBadUpdate(t, inbox, func() error {
		return inbox.SetStatus(context.TODO(), 10, conv.GetConvID(), chat1.ConversationStatus_BLOCKED)
	})
}

func TestInboxTlfFinalize(t *testing.T) {

	_, inbox, _ := setupInboxTest(t, "basic")

	// Create an inbox with a bunch of convos, merge it and read it back out
	numConvs := 10
	var convs []chat1.Conversation
	for i := numConvs - 1; i >= 0; i-- {
		convs = append(convs, makeConvo(gregor1.Time(i), 1, 1))
	}

	conv := convs[5]
	require.NoError(t, inbox.Merge(context.TODO(), 1, convs, nil, nil))
	require.NoError(t, inbox.TlfFinalize(context.TODO(), 2, []chat1.ConversationID{conv.GetConvID()},
		chat1.ConversationFinalizeInfo{ResetFull: "reset"}))
	_, res, _, err := inbox.Read(context.TODO(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, len(convs), len(res), "length")
	require.Equal(t, conv.GetConvID(), res[5].GetConvID(), "id")
	require.NotNil(t, res[5].Metadata.FinalizeInfo, "finalize info")

	validateBadUpdate(t, inbox, func() error {
		return inbox.TlfFinalize(context.TODO(), 10, []chat1.ConversationID{conv.GetConvID()},
			chat1.ConversationFinalizeInfo{ResetFull: "reset"})
	})
}
