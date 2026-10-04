package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func LoadAgentSession(apiURL string) (string, bool, error) {
	path, set := os.LookupEnv("MARUVO_AGENT_TOKEN_FILE")
	if !set {
		return "", false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", true, fmt.Errorf("open agent credential file: %w", err)
	}
	defer file.Close()

	var credential session
	if json.NewDecoder(io.LimitReader(file, 16<<10)).Decode(&credential) != nil ||
		credential.APIURL != apiURL || !strings.HasPrefix(credential.Token, "mru_agent_") {
		return "", true, fmt.Errorf("invalid agent credential file or API URL mismatch")
	}

	return credential.Token, true, nil
}
