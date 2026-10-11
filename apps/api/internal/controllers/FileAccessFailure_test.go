package controller

import (
	"bytes"
	"fmt"
	"testing"
)

func TestFileAccessReceiptFailureReturnsNoBytesAndRollsBackGuard(t *testing.T) {
	f := newFileAccessFixture(t)
	f.prepare(t, "refund", 201)
	constraint := fmt.Sprintf("reject_fixture_access_%d", f.postID)
	_, err := f.pool.Exec(f.ctx, fmt.Sprintf("ALTER TABLE workspace_file_accesses ADD CONSTRAINT %s CHECK (post_id <> %d) NOT VALID", constraint, f.postID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.pool.Exec(f.ctx, "ALTER TABLE workspace_file_accesses DROP CONSTRAINT "+constraint) })
	body := f.request(t, "GET", fmt.Sprintf("/posts/%d/files/%s", f.postID, f.file.ID.String()), f.tokens[0], nil, 500)
	if bytes.Contains(body, f.file.Content) {
		t.Fatal("receipt failure exposed file bytes")
	}
	f.checkAccess(t, 0, false)
	plan, err := f.controller.queries.GetPostSettlement(f.ctx, f.postID)
	if err != nil || plan.State != "prepared" {
		t.Fatal("receipt rollback invalidated a refund plan")
	}
}
