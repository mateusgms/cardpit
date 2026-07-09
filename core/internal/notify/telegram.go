package notify

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	buspkg "github.com/mateusgms/cardpit/core/internal/bus"
)

// tgClient is the thin seam over the Telegram Bot API so tests can fake it.
type tgClient interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *models.InlineKeyboardMarkup) (msgID int64, err error)
	EditMessageText(ctx context.Context, chatID, msgID int64, text string) error
	SendPhoto(ctx context.Context, chatID int64, png []byte, caption string) error
}

// tgNotifier implements Notifier over any tgClient. The first allowlisted
// chat is the "primary" one whose start message gets edited; every chat
// receives lifecycle messages.
type tgNotifier struct {
	client tgClient
	chats  []int64
}

func (t *tgNotifier) primary() int64 { return t.chats[0] }

func (t *tgNotifier) JobStarted(ctx context.Context, in StartInfo) (int64, error) {
	text := msgStart(in)
	ref, err := t.client.SendMessage(ctx, t.primary(), text, nil)
	if err != nil {
		return 0, err
	}
	for _, chat := range t.chats[1:] {
		t.client.SendMessage(ctx, chat, text, nil) // best-effort for extras
	}
	return ref, nil
}

func (t *tgNotifier) JobProgress(ctx context.Context, msgRef int64, in ProgressInfo) error {
	if msgRef == 0 {
		return nil
	}
	return t.client.EditMessageText(ctx, t.primary(), msgRef, msgProgress(in))
}

func (t *tgNotifier) JobCompleted(ctx context.Context, msgRef int64, in CompletedInfo, png []byte) error {
	text := msgCompleted(in)
	if msgRef != 0 {
		if err := t.client.EditMessageText(ctx, t.primary(), msgRef, text); err != nil {
			// Edit can fail if the message aged out; degrade to a new message.
			if _, err2 := t.client.SendMessage(ctx, t.primary(), text, nil); err2 != nil {
				return err2
			}
		}
	} else {
		if _, err := t.client.SendMessage(ctx, t.primary(), text, nil); err != nil {
			return err
		}
	}
	caption := captionCompleted(in)
	for _, chat := range t.chats {
		if png != nil {
			if err := t.client.SendPhoto(ctx, chat, png, caption); err != nil {
				// PNG is a bonus; the instruction must arrive regardless.
				if _, err2 := t.client.SendMessage(ctx, chat, caption, nil); err2 != nil {
					return err2
				}
			}
		} else {
			if _, err := t.client.SendMessage(ctx, chat, caption, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *tgNotifier) JobFailed(ctx context.Context, msgRef int64, in FailInfo) error {
	text := msgFailed(in)
	if msgRef != 0 {
		t.client.EditMessageText(ctx, t.primary(), msgRef, text) // best effort
	}
	var firstErr error
	for _, chat := range t.chats {
		if _, err := t.client.SendMessage(ctx, chat, text, nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *tgNotifier) AskUnknownCard(ctx context.Context, in buspkg.CardUnknown) error {
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "📥 Copiar", CallbackData: callbackData(in.Serial, "copy")},
		{Text: "🚫 Ignorar", CallbackData: callbackData(in.Serial, "ignore")},
		{Text: "🔕 Ignorar sempre", CallbackData: callbackData(in.Serial, "always_ignore")},
	}}}
	var firstErr error
	for _, chat := range t.chats {
		if _, err := t.client.SendMessage(ctx, chat, msgUnknown(in), kb); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *tgNotifier) DestMissing(ctx context.Context, in buspkg.DestMissing) error {
	var firstErr error
	for _, chat := range t.chats {
		if _, err := t.client.SendMessage(ctx, chat, msgDestMissing(in), nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *tgNotifier) Test(ctx context.Context) error {
	var firstErr error
	for _, chat := range t.chats {
		if _, err := t.client.SendMessage(ctx, chat, msgTest(), nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// --- callback data ----------------------------------------------------------

const callbackPrefix = "cardpit:card:"

func callbackData(serial, action string) string {
	return callbackPrefix + serial + ":" + action
}

func parseCallback(data string) (serial, action string, ok bool) {
	rest, found := strings.CutPrefix(data, callbackPrefix)
	if !found {
		return "", "", false
	}
	serial, action, found = strings.Cut(rest, ":")
	if !found || serial == "" {
		return "", "", false
	}
	switch action {
	case "copy", "ignore", "always_ignore":
		return serial, action, true
	}
	return "", "", false
}

var decisionLabel = map[string]string{
	"copy": "📥 Copiar", "ignore": "🚫 Ignorar", "always_ignore": "🔕 Ignorar sempre",
}

// --- real client + bot lifecycle ---------------------------------------------

type realClient struct{ b *bot.Bot }

func (c realClient) SendMessage(ctx context.Context, chatID int64, text string, kb *models.InlineKeyboardMarkup) (int64, error) {
	p := &bot.SendMessageParams{ChatID: chatID, Text: text}
	if kb != nil {
		p.ReplyMarkup = kb
	}
	m, err := c.b.SendMessage(ctx, p)
	if err != nil {
		return 0, err
	}
	return int64(m.ID), nil
}

func (c realClient) EditMessageText(ctx context.Context, chatID, msgID int64, text string) error {
	_, err := c.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: chatID, MessageID: int(msgID), Text: text,
	})
	return err
}

func (c realClient) SendPhoto(ctx context.Context, chatID int64, png []byte, caption string) error {
	_, err := c.b.SendPhoto(ctx, &bot.SendPhotoParams{
		ChatID:  chatID,
		Photo:   &models.InputFileUpload{Filename: "cardpit-report.png", Data: bytes.NewReader(png)},
		Caption: caption,
	})
	return err
}

// buildTelegram constructs the real bot, wires the callback handler (inline
// decision buttons, RF-04.6/RF-04.7) and starts long polling.
func (d *Dispatcher) buildTelegram(ctx context.Context, token string, chats []int64) (Notifier, func(), error) {
	allow := make(map[int64]bool, len(chats))
	for _, c := range chats {
		allow[c] = true
	}

	handler := func(hctx context.Context, b *bot.Bot, update *models.Update) {
		cq := update.CallbackQuery
		if cq == nil {
			return
		}
		defer b.AnswerCallbackQuery(hctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID})
		msg := cq.Message.Message
		if msg == nil || !allow[msg.Chat.ID] {
			return // chat not allowlisted: ignore silently (RF-04.7)
		}
		serial, action, ok := parseCallback(cq.Data)
		if !ok {
			return
		}
		d.log.Info("notify: telegram decision", "serial", serial, "action", action)
		d.bus.Publish(buspkg.Event{Topic: buspkg.TopicCardDecision, Payload: buspkg.CardDecision{
			Serial: serial, Action: action,
		}})
		// Freeze the question message showing what was chosen.
		b.EditMessageText(hctx, &bot.EditMessageTextParams{
			ChatID: msg.Chat.ID, MessageID: msg.ID,
			Text: msg.Text + "\n\n→ " + decisionLabel[action],
		})
	}

	// /start and /chatid reply to anyone (not restricted to the allowlist) so
	// users can discover their chat_id before they are configured.
	statusHandler := func(hctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil {
			return
		}
		chatID := update.Message.Chat.ID
		info := d.buildStatusInfo(hctx, chatID)
		b.SendMessage(hctx, &bot.SendMessageParams{ChatID: chatID, Text: msgBotStatus(info)})
	}

	b, err := bot.New(token, bot.WithDefaultHandler(handler))
	if err != nil {
		return nil, nil, fmt.Errorf("telegram: %w", err)
	}
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, statusHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/chatid", bot.MatchTypePrefix, statusHandler)
	bctx, cancel := context.WithCancel(ctx)
	go b.Start(bctx)
	return &tgNotifier{client: realClient{b}, chats: chats}, cancel, nil
}
