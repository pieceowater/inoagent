package telegram

// Only the slice of the Bot API the bridge actually uses. Unknown fields in
// Telegram's responses are ignored by encoding/json, so this stays valid as
// the API grows.

type Update struct {
	UpdateID      int64              `json:"update_id"`
	Message       *Message           `json:"message,omitempty"`
	CallbackQuery *CallbackQuery     `json:"callback_query,omitempty"`
	MyChatMember  *ChatMemberUpdated `json:"my_chat_member,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// DisplayName is how a person is shown to the agent and in approval messages.
func (u User) DisplayName() string {
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	if u.Username != "" {
		name += " (@" + u.Username + ")"
	}
	return name
}

type Chat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"` // private, group, supergroup, channel
	Title   string `json:"title,omitempty"`
	IsForum bool   `json:"is_forum,omitempty"`
}

func (c Chat) IsGroup() bool { return c.Type == "group" || c.Type == "supergroup" }

type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"` // in UTF-16 code units
	Length int    `json:"length"`
	User   *User  `json:"user,omitempty"`
}

type Message struct {
	MessageID       int             `json:"message_id"`
	MessageThreadID int             `json:"message_thread_id,omitempty"`
	IsTopicMessage  bool            `json:"is_topic_message,omitempty"`
	From            *User           `json:"from,omitempty"`
	Chat            Chat            `json:"chat"`
	Date            int64           `json:"date"`
	Text            string          `json:"text,omitempty"`
	Caption         string          `json:"caption,omitempty"`
	Entities        []MessageEntity `json:"entities,omitempty"`
	ReplyToMessage  *Message        `json:"reply_to_message,omitempty"`
	// Service messages about the forum topic itself.
	ForumTopicCreated *ForumTopic `json:"forum_topic_created,omitempty"`
	ForumTopicEdited  *ForumTopic `json:"forum_topic_edited,omitempty"`
	ForwardOrigin     *struct {
		Type string `json:"type"`
	} `json:"forward_origin,omitempty"`

	Photo     []File `json:"photo,omitempty"` // the same picture in several sizes, smallest first
	Document  *File  `json:"document,omitempty"`
	Voice     *File  `json:"voice,omitempty"`
	VideoNote *File  `json:"video_note,omitempty"`
	Video     *File  `json:"video,omitempty"`
	Audio     *File  `json:"audio,omitempty"`
}

// File is the part every Telegram attachment type shares.
type File struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	Duration int    `json:"duration,omitempty"`
	FilePath string `json:"file_path,omitempty"` // only in getFile's answer
}

// Thread is the topic a message belongs to, 0 for a chat without topics and
// for the forum's General topic (Telegram omits the id there, and a reply
// sent without one lands in General — which is exactly what we want).
func (m *Message) Thread() int {
	if m.IsTopicMessage {
		return m.MessageThreadID
	}
	return 0
}

// HasAttachment: a file someone might expect the agent to read. Stickers and
// service messages (topic renamed, member joined) don't count — in a group
// chat they are conversation, not requests.
type ForumTopic struct {
	Name string `json:"name,omitempty"` // empty in an edit that only changed the icon
}

// TopicName is the name of the forum topic, when this message carries it:
// the topic's own created/renamed service message, or — what most messages in
// a topic are — a message that implicitly replies to the topic's creation.
func (m *Message) TopicName() string {
	switch {
	case m.ForumTopicEdited != nil && m.ForumTopicEdited.Name != "":
		return m.ForumTopicEdited.Name
	case m.ForumTopicCreated != nil:
		return m.ForumTopicCreated.Name
	case m.ReplyToMessage != nil && m.ReplyToMessage.ForumTopicCreated != nil:
		return m.ReplyToMessage.ForumTopicCreated.Name
	}
	return ""
}

func (m *Message) HasAttachment() bool {
	return len(m.Photo) > 0 || m.Document != nil || m.Voice != nil ||
		m.VideoNote != nil || m.Video != nil || m.Audio != nil
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type ChatMember struct {
	Status   string `json:"status"` // creator, administrator, member, restricted, left, kicked
	User     User   `json:"user"`
	IsMember bool   `json:"is_member,omitempty"` // only meaningful for "restricted"
}

// Present reports whether the user is currently in the chat.
func (m ChatMember) Present() bool {
	switch m.Status {
	case "creator", "administrator", "member":
		return true
	case "restricted":
		return m.IsMember
	}
	return false
}

type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	From          User       `json:"from"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// ReplyKeyboardMarkup replaces the phone keyboard with buttons — the icon
// next to the paperclip. Pressing one sends its text as a normal message.
type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	IsPersistent   bool               `json:"is_persistent,omitempty"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
}

type KeyboardButton struct {
	Text string `json:"text"`
}

type ReplyParameters struct {
	MessageID                int  `json:"message_id"`
	AllowSendingWithoutReply bool `json:"allow_sending_without_reply,omitempty"`
}

type SendMessageParams struct {
	ChatID          int64            `json:"chat_id"`
	MessageThreadID int              `json:"message_thread_id,omitempty"`
	Text            string           `json:"text"`
	ParseMode       string           `json:"parse_mode,omitempty"`
	ReplyParameters *ReplyParameters `json:"reply_parameters,omitempty"`
	// *InlineKeyboardMarkup (buttons under the message) or
	// *ReplyKeyboardMarkup (buttons in place of the keyboard); nil for none.
	ReplyMarkup any                 `json:"reply_markup,omitempty"`
	LinkPreview *LinkPreviewOptions `json:"link_preview_options,omitempty"`
}

type LinkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type EditMessageTextParams struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int                   `json:"message_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}
