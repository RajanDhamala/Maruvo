package providers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ChatScope struct {
	Profile, APIURL, Account string
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Conversation struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Directory string        `json:"directory"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []ChatMessage `json:"messages"`
	Lines     []string      `json:"lines"`
	Usage     string        `json:"usage,omitempty"`
	Draft     string        `json:"draft,omitempty"`
	Pending   bool          `json:"pending"`
	Archived  bool          `json:"archived,omitempty"`
}

type ConversationSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Directory string    `json:"directory"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Archived  bool      `json:"archived"`
}

const maxConversationBytes = 2 << 20

var conversationWrites sync.Mutex

func chatHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func chatID(value string, size int) bool {
	_, err := hex.DecodeString(value)
	return len(value) == size && err == nil && strings.ToLower(value) == value
}

func NewConversation(directory, title, provider, model string) (Conversation, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return Conversation{}, err
	}

	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Conversation{}, err
	}

	title = strings.Join(strings.Fields(title), " ")
	if chars := []rune(title); len(chars) > 80 {
		title = string(chars[:77]) + "…"
	}

	now := time.Now().UTC()

	return Conversation{ID: hex.EncodeToString(id[:]), Title: title, Directory: directory,
		Provider: provider, Model: model, CreatedAt: now, UpdatedAt: now}, nil
}

func chatRoot(scope ChatScope) (*os.Root, error) {
	providerDir, err := configDirectory(scope.Profile)
	if err != nil {
		return nil, err
	}

	base := filepath.Join(filepath.Dir(providerDir), "chats")
	if err = privateDirectory(base); err != nil {
		return nil, err
	}

	account := filepath.Join(base, chatHash(strings.TrimRight(scope.APIURL, "/")+"\x00"+scope.Account))
	if err = privateDirectory(account); err != nil {
		return nil, err
	}

	return os.OpenRoot(account)
}

func readConversation(root *os.Root, directory, id string) (Conversation, error) {
	var chat Conversation
	if !chatID(directory, 64) || !chatID(id, 32) {
		return chat, errors.New("invalid conversation ID")
	}

	dirInfo, err := root.Lstat(directory)
	if err != nil {
		return chat, err
	}

	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0077 != 0 || dirInfo.Mode()&os.ModeSymlink != 0 {
		return chat, errors.New("chat directory must be private and cannot be a symlink")
	}

	path := directory + "/" + id + ".json"

	info, err := root.Lstat(path)
	if err != nil {
		return chat, err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxConversationBytes {
		return chat, errors.New("chat files must be private regular files of at most 2 MiB")
	}

	f, err := root.Open(path)
	if err != nil {
		return chat, err
	}
	defer f.Close()

	d := json.NewDecoder(io.LimitReader(f, maxConversationBytes+1))
	d.DisallowUnknownFields()

	if d.Decode(&chat) != nil || d.Decode(new(any)) != io.EOF || chat.ID != id ||
		!filepath.IsAbs(chat.Directory) || chatHash(chat.Directory) != directory {
		return Conversation{}, errors.New("invalid saved conversation")
	}

	for _, message := range chat.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return Conversation{}, errors.New("invalid saved chat message")
		}
	}

	return chat, nil
}

func SaveConversation(scope ChatScope, chat Conversation) error {
	if !chatID(chat.ID, 32) || !filepath.IsAbs(chat.Directory) {
		return errors.New("invalid conversation")
	}

	data, err := json.Marshal(chat)
	if err != nil {
		return err
	}

	if len(data) > maxConversationBytes {
		return errors.New("conversation exceeds the 2 MiB local history limit; start a new chat")
	}

	conversationWrites.Lock()
	defer conversationWrites.Unlock()

	root, err := chatRoot(scope)
	if err != nil {
		return err
	}
	defer root.Close()

	directory := chatHash(chat.Directory)
	if err = root.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	info, err := root.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("chat directory must be private and cannot be a symlink")
	}

	current, err := readConversation(root, directory, chat.ID)
	if err == nil && current.UpdatedAt.After(chat.UpdatedAt) {
		return nil
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	temporary := directory + "/.chat-" + rand.Text()

	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)

	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}

	closeErr := f.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return root.Rename(temporary, directory+"/"+chat.ID+".json")
}

func LoadConversation(scope ChatScope, directory, id string) (Conversation, error) {
	root, err := chatRoot(scope)
	if err != nil {
		return Conversation{}, err
	}
	defer root.Close()

	return readConversation(root, chatHash(filepath.Clean(directory)), id)
}

func ListConversations(scope ChatScope) ([]ConversationSummary, error) {
	root, err := chatRoot(scope)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	type candidate struct {
		directory, id string
		modified      time.Time
	}

	var files []candidate

	directories, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}

	for _, directory := range directories {
		if !directory.IsDir() || !chatID(directory.Name(), 64) {
			continue
		}

		entries, err := fs.ReadDir(root.FS(), directory.Name())
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".json")
			if entry.Type().IsRegular() && chatID(id, 32) && strings.HasSuffix(entry.Name(), ".json") {
				info, err := entry.Info()
				if err == nil {
					files = append(files, candidate{directory.Name(), id, info.ModTime()})
				}
			}
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modified.After(files[j].modified) })

	chats := []ConversationSummary{}

	var readErr error

	for _, file := range files[:min(200, len(files))] {
		chat, err := readConversation(root, file.directory, file.id)
		if err != nil {
			readErr = err
			continue
		}

		chats = append(chats, SummarizeConversation(chat))
	}

	return chats, readErr
}

func SummarizeConversation(chat Conversation) ConversationSummary {
	return ConversationSummary{ID: chat.ID, Title: chat.Title, Directory: chat.Directory,
		Provider: chat.Provider, Model: chat.Model, CreatedAt: chat.CreatedAt,
		UpdatedAt: chat.UpdatedAt, Archived: chat.Archived}
}

func (c *Client) SafeChatText(text string, marketplace *Marketplace) string {
	if c != nil && c.key != "" {
		text = strings.ReplaceAll(text, c.key, "[API key redacted]")
	}

	return marketplace.redact(text)
}

func ConversationHistory(chat Conversation) []Message {
	history := make([]Message, 0, len(chat.Messages))
	for _, message := range chat.Messages {
		history = append(history, Message{Role: message.Role, Content: message.Content})
	}

	return history
}
