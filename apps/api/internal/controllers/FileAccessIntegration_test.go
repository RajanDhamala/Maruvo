package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type fileAccessRPC struct {
	deadlineRPC
	unsigned string
	submits  atomic.Int32
}

func (s *fileAccessRPC) PrepareSettlement(context.Context, *pb.PrepareSettlementRequest) (*pb.PreparedSettlement, error) {
	return &pb.PreparedSettlement{Transaction: s.unsigned, LastValidBlockHeight: 100, FeeLamports: 5000}, nil
}
func (*fileAccessRPC) CheckSettlement(_ context.Context, r *pb.CheckSettlementRequest) (*pb.SettlementState, error) {
	state := "prepared"
	if r.Signature != "" {
		state = "pending"
	}
	return &pb.SettlementState{State: state}, nil
}
func (s *fileAccessRPC) SubmitEscrow(context.Context, *pb.SubmitEscrowRequest) (*pb.SubmittedEscrow, error) {
	s.submits.Add(1)
	return &pb.SubmittedEscrow{}, nil
}

type fileAccessFixture struct {
	*agentAccessFixture
	rpc    *fileAccessRPC
	signed string
	file   db.WorkspaceFile
}

func newFileAccessFixture(t *testing.T) *fileAccessFixture {
	t.Helper()
	f := newAgentAccessFixture(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A signed fixture envelope exercises the API's signature check; no chain RPC.
	message := []byte("isolated settlement fixture")
	unsigned := append(make([]byte, 65), message...)
	unsigned[0] = 1
	signed := append([]byte{}, unsigned...)
	copy(signed[1:65], ed25519.Sign(private, message))
	rpc := &fileAccessRPC{unsigned: base64.StdEncoding.EncodeToString(unsigned)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterSolanaServiceServer(server, rpc)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///file-access", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f.controller.rpc = conn
	mux := http.NewServeMux()
	mux.HandleFunc("POST /posts/{id}/settle", f.controller.Auth(f.controller.PreparePostSettlement))
	mux.HandleFunc("POST /posts/{id}/settle/submit", f.controller.Auth(f.controller.SubmitPostSettlement))
	mux.Handle("/", f.server.Config.Handler)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	wallet := utils.Base58(public)
	if _, err = f.pool.Exec(f.ctx, "INSERT INTO wallets(user_id,address) VALUES($1,$2)", f.users[0], wallet); err != nil {
		t.Fatal(err)
	}
	if _, err = f.controller.queries.SavePostEscrow(f.ctx, db.SavePostEscrowParams{PostID: f.postID,
		Address: uuid.NewString(), ProgramID: "fixture", Reviewer: wallet, Network: "localnet", Transaction: "fixture", AgreementVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE post_escrows SET state='confirmed' WHERE post_id=$1", f.postID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE posts SET status='in_progress', poster_wallet=$2 WHERE id=$1", f.postID, wallet); err != nil {
		t.Fatal(err)
	}
	result := &fileAccessFixture{agentAccessFixture: f, rpc: rpc, signed: base64.StdEncoding.EncodeToString(signed)}
	result.file = result.addFile(t, 1, "output")
	if _, err = f.pool.Exec(f.ctx, "UPDATE post_workspaces SET review_state='submitted',submission_version=1,submitted_at=NOW(),delivery_files=ARRAY[$2::uuid] WHERE post_id=$1", f.postID, result.file.ID); err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *fileAccessFixture) addFile(t *testing.T, owner int, purpose string) db.WorkspaceFile {
	t.Helper()
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	content := []byte("private fixture source\n")
	sum := sha256.Sum256(content)
	_, err := f.pool.Exec(f.ctx, "INSERT INTO workspace_files(id,post_id,uploaded_by,name,size,sha256,content,purpose) VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
		id, f.postID, f.users[owner], "result-"+id.String()+".go", len(content), hex.EncodeToString(sum[:]), content, purpose)
	if err != nil {
		t.Fatal(err)
	}
	file, err := f.controller.queries.GetWorkspaceFile(f.ctx, db.GetWorkspaceFileParams{ID: id, PostID: f.postID})
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func (f *fileAccessFixture) prepare(t *testing.T, action string, status int) []byte {
	return f.request(t, "POST", fmt.Sprintf("/posts/%d/settle", f.postID), f.tokens[0], reviewPayload{Action: action, Note: "fixture review", SubmissionVersion: 1}, status)
}

func (f *fileAccessFixture) checkAccess(t *testing.T, receipts int, blocked bool) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM workspace_file_accesses WHERE post_id=$1", f.postID).Scan(&count); err != nil || count != receipts {
		t.Fatalf("receipts=%d, expected %d: %v", count, receipts, err)
	}
	ws, err := f.controller.queries.GetWorkspace(f.ctx, f.postID)
	if err != nil || ws.RefundBlocked != blocked {
		t.Fatalf("refund guard=%v expected %v: %v", ws.RefundBlocked, blocked, err)
	}
}

func TestDeliveryAccessBlocksPreparedRefundAndSurvivesRevision(t *testing.T) {
	f := newFileAccessFixture(t)
	f.prepare(t, "refund", 201)
	path := fmt.Sprintf("/posts/%d/files/%s", f.postID, f.file.ID.String())
	for range 2 {
		if body := f.request(t, "GET", path, f.tokens[0], nil, 200); !bytes.Equal(body, f.file.Content) {
			t.Fatal("download bytes changed")
		}
	}
	f.checkAccess(t, 1, true)
	var events int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM workspace_events WHERE post_id=$1 AND kind='file.accessed'", f.postID).Scan(&events); err != nil || events != 1 {
		t.Fatal("retry duplicated access event")
	}
	var receipt db.WorkspaceFileAccess
	if err := f.pool.QueryRow(f.ctx, "SELECT submission_version,sha256,size,protects_refund FROM workspace_file_accesses WHERE post_id=$1", f.postID).Scan(&receipt.SubmissionVersion, &receipt.Sha256, &receipt.Size, &receipt.ProtectsRefund); err != nil || receipt.SubmissionVersion != 1 || receipt.Sha256 != f.file.Sha256 || receipt.Size != f.file.Size || !receipt.ProtectsRefund {
		t.Fatal("exact file receipt missing", err)
	}
	f.prepare(t, "refund", 409)
	f.request(t, "POST", fmt.Sprintf("/posts/%d/settle/submit", f.postID), f.tokens[0], map[string]string{"transaction": f.signed}, 409)
	if f.rpc.submits.Load() != 0 {
		t.Fatal("blocked refund reached RPC")
	}
	saved, err := f.controller.queries.GetPostSettlement(f.ctx, f.postID)
	if err != nil || saved.State != "expired" {
		t.Fatal("earlier refund plan still active")
	}
	f.request(t, "POST", fmt.Sprintf("/posts/%d/review/changes", f.postID), f.tokens[0], reviewPayload{Note: "revise output", SubmissionVersion: 1}, 201)
	file := f.addFile(t, 1, "output")
	f.request(t, "POST", fmt.Sprintf("/posts/%d/submit", f.postID), f.tokens[1], map[string]any{"note": "revised", "submission_version": 1, "files": []string{file.ID.String()}, "input_files": []string{}}, 201)
	f.checkAccess(t, 1, true)
	// Fresh query bindings model recovery with no in-memory download state.
	ws, err := db.New(f.pool).GetWorkspace(f.ctx, f.postID)
	if err != nil || ws.SubmissionVersion != 2 || !ws.RefundBlocked {
		t.Fatal("restart/revision lost guard")
	}
	f.request(t, "POST", fmt.Sprintf("/posts/%d/settle", f.postID), f.tokens[0], reviewPayload{Action: "release", Note: "accept revision", SubmissionVersion: 2}, 201)
}

func TestFileAccessAuthorizationAndScopedSocket(t *testing.T) {
	f := newFileAccessFixture(t)
	path := fmt.Sprintf("/posts/%d/files/%s", f.postID, f.file.ID.String())
	f.request(t, "GET", path, f.tokens[2], nil, 403)
	f.request(t, "GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, uuid.NewString()), f.tokens[0], nil, 404)
	f.checkAccess(t, 0, false)
	f.request(t, "GET", path, f.tokens[1], nil, 200)
	input := f.addFile(t, 0, "input")
	f.request(t, "GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, input.ID.String()), f.tokens[0], nil, 200)
	f.checkAccess(t, 2, false)
	token, grantID := f.grant(t, 0, "read")
	conn := f.socket(t, token, fmt.Sprintf("/ws/files?post_id=%d", f.postID))
	if err := conn.WriteJSON(fileRequest{Action: "download", ID: f.file.ID.String()}); err != nil {
		t.Fatal(err)
	}
	var frame WsResponse
	if err := conn.ReadJSON(&frame); err != nil || frame.Event != "file.meta" {
		t.Fatal("socket metadata failed", err)
	}
	// Receipt must already exist before the first binary frame is read.
	f.checkAccess(t, 3, true)
	kind, body, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(body, f.file.Content) {
		t.Fatal("socket contents failed", err)
	}
	if err = conn.ReadJSON(&frame); err != nil || frame.Event != "file.complete" {
		t.Fatal("socket completion failed")
	}
	var attribution bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM workspace_file_accesses WHERE post_id=$1 AND agent_grant_id=$2 AND user_id=$3)", f.postID, grantID, f.users[0]).Scan(&attribution); err != nil || !attribution {
		t.Fatal("requester harness access was not attributed")
	}
	f.request(t, "POST", "/agents/"+grantID+"/revoke", f.tokens[0], nil, 200)
	f.request(t, "GET", path, token, nil, 401)
}

func TestRefundWinsBeforeDownloadAndBlocksSocket(t *testing.T) {
	f := newFileAccessFixture(t)
	f.prepare(t, "refund", 201)
	f.request(t, "POST", fmt.Sprintf("/posts/%d/settle/submit", f.postID), f.tokens[0], map[string]string{"transaction": f.signed}, 202)
	for _, state := range []string{"pending", "confirmed"} {
		if _, err := f.pool.Exec(f.ctx, "UPDATE post_settlements SET state=$2 WHERE post_id=$1", f.postID, state); err != nil {
			t.Fatal(err)
		}
		f.request(t, "GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, f.file.ID.String()), f.tokens[0], nil, 409)
		conn := f.socket(t, f.tokens[0], fmt.Sprintf("/ws/files?post_id=%d", f.postID))
		if err := conn.WriteJSON(fileRequest{Action: "download", ID: f.file.ID.String()}); err != nil {
			t.Fatal(err)
		}
		var frame struct {
			Event string
			Data  json.RawMessage
		}
		if err := conn.ReadJSON(&frame); err != nil || frame.Event != "error" || !bytes.Contains(frame.Data, []byte("409")) {
			t.Fatal("refund exposed socket bytes")
		}
		_ = conn.Close()
	}
	f.checkAccess(t, 0, false)
}

func TestConcurrentRefundAndDownload(t *testing.T) {
	for range 8 {
		f := newFileAccessFixture(t)
		f.prepare(t, "refund", 201)
		start := make(chan struct{})
		results := make(chan int, 2)
		for _, op := range []struct{ method, path, body string }{
			{"GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, f.file.ID.String()), ""},
			{"POST", fmt.Sprintf("/posts/%d/settle/submit", f.postID), `{"transaction":"` + f.signed + `"}`},
		} {
			go func() {
				<-start
				r, err := http.NewRequestWithContext(f.ctx, op.method, f.server.URL+op.path, strings.NewReader(op.body))
				if err != nil {
					results <- -1
					return
				}
				r.Header.Set("Authorization", "Bearer "+f.tokens[0])
				response, err := http.DefaultClient.Do(r)
				if err != nil {
					results <- -1
					return
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				results <- response.StatusCode
			}()
		}
		close(start)
		first, second := <-results, <-results
		if first != 409 && second != 409 {
			t.Fatalf("both competing operations succeeded: %d/%d", first, second)
		}
		if first == 200 || second == 200 {
			f.checkAccess(t, 1, true)
		} else if first == 202 || second == 202 {
			f.checkAccess(t, 0, false)
		} else {
			t.Fatalf("neither operation succeeded: %d/%d", first, second)
		}
	}
}

func TestSharedWorkerOutputBeforeSubmissionProtectsRefund(t *testing.T) {
	f := newFileAccessFixture(t)
	file := f.addFile(t, 1, "shared")
	f.request(t, "GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, file.ID.String()), f.tokens[0], nil, 200)
	f.checkAccess(t, 1, true)
	var version int64
	if err := f.pool.QueryRow(f.ctx, "SELECT submission_version FROM workspace_file_accesses WHERE post_id=$1", f.postID).Scan(&version); err != nil || version != 0 {
		t.Fatal("unsubmitted file claimed a delivery version")
	}
	f.prepare(t, "refund", 409)
}
