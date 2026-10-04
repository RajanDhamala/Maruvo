package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/internal/utils"
)

func TestDescriptionTextLimits(t *testing.T) {
	for _, test := range []struct {
		text  string
		valid bool
	}{
		{"Line one\n\tIndentation: café", true},
		{strings.Repeat("a", maxDescriptionCharacters), true},
		{strings.Repeat("a", maxDescriptionCharacters+1), false},
		{strings.Repeat("😀", maxDescriptionBytes/4), true},
		{strings.Repeat("😀", maxDescriptionBytes/4+1), false},
		{"text\x00binary", false},
		{"text\x1b[31m", false},
		{string([]byte{255}), false},
	} {
		if validTaskText(test.text, maxDescriptionCharacters, maxDescriptionBytes) != test.valid {
			t.Errorf("%d bytes: want valid=%v", len(test.text), test.valid)
		}
	}
}

func TestInvalidDescriptionIsRejectedBeforeDatabase(t *testing.T) {
	for _, description := range []string{"", " \n", "text\x00binary", strings.Repeat("a", 12001), strings.Repeat("😀", 8193)} {
		payload, _ := json.Marshal(map[string]string{"title": "A task", "description": description})
		request := httptest.NewRequest("POST", "/posts/create", strings.NewReader(string(payload)))
		request = request.WithContext(
			context.WithValue(request.Context(), utils.UserKey, &utils.UserJWT{ID: "1"}),
		)
		response := httptest.NewRecorder()
		(&Controller{}).CreatePost(response, request)

		if response.Code != 400 {
			t.Fatalf("invalid description: got %d", response.Code)
		}
	}
}

func TestDeliveryTextLimitsBeforeAuthorization(t *testing.T) {
	for _, test := range []struct {
		text string
		code int
	}{
		{strings.Repeat("<", 4000), http.StatusUnauthorized},
		{strings.Repeat("😀", 4000), http.StatusUnauthorized},
		{"", http.StatusBadRequest},
		{"text\x00binary", http.StatusBadRequest},
		{strings.Repeat("a", 4001), http.StatusBadRequest},
	} {
		payload, _ := json.Marshal(map[string]string{"note": test.text})
		request := httptest.NewRequest("POST", "/posts/1/submit", strings.NewReader(string(payload)))
		response := httptest.NewRecorder()
		(&Controller{}).SubmitWorkspace(response, request)

		if response.Code != test.code {
			t.Fatalf("delivery of %d bytes: got %d, want %d", len(test.text), response.Code, test.code)
		}
	}
}
