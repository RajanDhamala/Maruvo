package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type ConversationStore struct {
	Scope  ChatScope
	Client *api.Client
	Token  string
}

func (s ConversationStore) online() bool {
	return s.Client != nil && s.Token != "" && s.Scope.Account != ""
}

func (s ConversationStore) Save(ctx context.Context, chat Conversation) error {
	localErr := SaveConversation(s.Scope, chat)
	if !s.online() {
		return localErr
	}

	remoteErr := s.Client.SaveAgentChat(ctx, s.Token, s.Scope.Profile, chat.ID, chat)
	if remoteErr != nil {
		remoteErr = fmt.Errorf("database sync failed: %w", remoteErr)
	}

	return errors.Join(localErr, remoteErr)
}

func (s ConversationStore) List(ctx context.Context) ([]ConversationSummary, error) {
	local, localErr := ListConversations(s.Scope)
	if !s.online() {
		return local, localErr
	}

	var remote []ConversationSummary
	if err := s.Client.AgentChats(ctx, s.Token, s.Scope.Profile, &remote); err != nil {
		return local, fmt.Errorf("database unavailable; showing local history: %w", err)
	}

	merged := make(map[string]ConversationSummary, len(local)+len(remote))
	for _, chat := range remote {
		merged[chat.ID] = chat
	}

	for _, chat := range local {
		current, exists := merged[chat.ID]
		if exists && !chat.UpdatedAt.After(current.UpdatedAt) {
			continue
		}

		merged[chat.ID] = chat

		snapshot, err := LoadConversation(s.Scope, chat.Directory, chat.ID)
		if err == nil {
			err = s.Client.SaveAgentChat(ctx, s.Token, s.Scope.Profile, chat.ID, snapshot)
		}

		if err != nil {
			localErr = fmt.Errorf("local history retained; database sync failed: %w", err)
		}

		if ctx.Err() != nil {
			break
		}
	}

	chats := make([]ConversationSummary, 0, len(merged))
	for _, chat := range merged {
		chats = append(chats, chat)
	}

	sort.Slice(chats, func(i, j int) bool { return chats[i].UpdatedAt.After(chats[j].UpdatedAt) })

	return chats[:min(200, len(chats))], localErr
}

func (s ConversationStore) Load(ctx context.Context, directory, id string) (Conversation, error) {
	local, localErr := LoadConversation(s.Scope, directory, id)
	if !s.online() {
		return local, localErr
	}

	var remote Conversation

	err := s.Client.AgentChat(ctx, s.Token, s.Scope.Profile, id, &remote)
	if err != nil {
		if localErr == nil {
			return local, nil
		}

		return Conversation{}, err
	}

	if remote.ID != id || remote.Directory != directory {
		return Conversation{}, errors.New("saved chat identity does not match the selected session")
	}

	if localErr == nil && local.UpdatedAt.After(remote.UpdatedAt) {
		return local, nil
	}

	_ = SaveConversation(s.Scope, remote)

	return remote, nil
}
