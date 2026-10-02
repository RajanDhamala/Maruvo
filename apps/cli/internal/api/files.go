package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const fileChunkSize = 64 << 10

func (c *Client) fileSocket(
	ctx context.Context,
	token string,
	postID int64,
) (*websocket.Conn, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)

	u, err := url.Parse(c.baseURL + "/ws/files")
	if err != nil {
		cancel()
		return nil, nil, err
	}

	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}

	q := u.Query()
	q.Set("post_id", strconv.FormatInt(postID, 10))
	u.RawQuery = q.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}

	conn, response, err := dialer.DialContext(
		ctx,
		u.String(),
		http.Header{"Authorization": []string{"Bearer " + token}},
	)
	if err != nil {
		cancel()

		if response != nil {
			response.Body.Close()

			return nil, nil, &Error{
				StatusCode: response.StatusCode,
				Message:    "file connection failed: " + response.Status,
			}
		}

		return nil, nil, err
	}

	conn.SetReadLimit(fileChunkSize)
	conn.SetReadDeadline(time.Now().Add(65 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	stop := context.AfterFunc(ctx, func() { conn.Close() })

	return conn, func() { stop(); cancel(); conn.Close() }, nil
}

func readFileControl(conn *websocket.Conn, expected string) (WorkspaceFile, error) {
	var frame StreamFrame
	if err := conn.ReadJSON(&frame); err != nil {
		return WorkspaceFile{}, err
	}

	if frame.Event == "error" {
		var failure struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(frame.Data, &failure); err != nil {
			return WorkspaceFile{}, err
		}

		return WorkspaceFile{}, &Error{StatusCode: failure.Status, Message: failure.Message}
	}

	if frame.Event != expected {
		return WorkspaceFile{}, fmt.Errorf("unexpected file response: %s", frame.Event)
	}

	var file WorkspaceFile

	err := json.Unmarshal(frame.Data, &file)

	return file, err
}

func (c *Client) UploadFile(ctx context.Context, token string, id int64, path string) error {
	_, err := c.UploadTaskFile(ctx, token, id, path, "shared")
	return err
}

func (c *Client) UploadTaskFile(
	ctx context.Context,
	token string,
	id int64,
	path, purpose string,
) (WorkspaceFile, error) {
	file, err := os.Open(localPath(path))
	if err != nil {
		return WorkspaceFile{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return WorkspaceFile{}, err
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 10<<20 {
		return WorkspaceFile{}, errors.New("choose a regular file between 1 byte and 10 MiB")
	}

	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil {
		return WorkspaceFile{}, err
	}

	return c.UploadBytes(ctx, token, id, filepath.Base(path), purpose, data)
}

func (c *Client) UploadBytes(
	ctx context.Context,
	token string,
	id int64,
	name, purpose string,
	data []byte,
) (WorkspaceFile, error) {
	if len(data) == 0 || len(data) > 10<<20 {
		return WorkspaceFile{}, errors.New("file must be between 1 byte and 10 MiB")
	}

	conn, close, err := c.fileSocket(ctx, token, id)
	if err != nil {
		return WorkspaceFile{}, err
	}
	defer close()

	hash := sha256.Sum256(data)
	hexdigest := hex.EncodeToString(hash[:])

	err = conn.WriteJSON(
		map[string]any{
			"action":  "upload",
			"name":    name,
			"size":    len(data),
			"sha256":  hexdigest,
			"purpose": purpose,
		},
	)
	if err != nil {
		return WorkspaceFile{}, err
	}

	for offset := 0; offset < len(data); offset += fileChunkSize {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

		if err := conn.WriteMessage(
			websocket.BinaryMessage,
			data[offset:min(len(data), offset+fileChunkSize)],
		); err != nil {
			return WorkspaceFile{}, err
		}
	}

	file, err := readFileControl(conn, "file.complete")
	if err == nil &&
		(file.SHA256 != hexdigest || file.Size != int64(len(data)) || file.Name != name || file.ID == "") {
		err = errors.New("file upload acknowledgement does not match")
	}

	return file, err
}

func (c *Client) DownloadFile(
	ctx context.Context,
	token string,
	id int64,
	file WorkspaceFile,
	destination string,
) error {
	conn, close, err := c.fileSocket(ctx, token, id)
	if err != nil {
		return err
	}
	defer close()

	if err = conn.WriteJSON(map[string]string{"action": "download", "id": file.ID}); err != nil {
		return err
	}

	meta, err := readFileControl(conn, "file.meta")
	if err != nil {
		return err
	}

	if meta.ID != file.ID || meta.Size != file.Size || meta.SHA256 != file.SHA256 || meta.Size <= 0 ||
		meta.Size > 10<<20 {
		return errors.New("file metadata does not match")
	}

	output, err := os.OpenFile(localPath(destination), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}

	success := false

	defer func() {
		output.Close()

		if !success {
			os.Remove(localPath(destination))
		}
	}()

	hash := sha256.New()

	var size int64
	for size < meta.Size {
		conn.SetReadDeadline(time.Now().Add(65 * time.Second))

		kind, chunk, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		if kind != websocket.BinaryMessage || len(chunk) == 0 || size+int64(len(chunk)) > meta.Size {
			return errors.New("invalid file chunk")
		}

		if _, err = output.Write(chunk); err != nil {
			return err
		}

		hash.Write(chunk)
		size += int64(len(chunk))
	}

	ack, err := readFileControl(conn, "file.complete")
	if err != nil {
		return err
	}

	if ack.ID != file.ID || hex.EncodeToString(hash.Sum(nil)) != meta.SHA256 {
		return errors.New("download integrity check failed")
	}

	if err = output.Close(); err != nil {
		return err
	}

	success = true

	return nil
}
