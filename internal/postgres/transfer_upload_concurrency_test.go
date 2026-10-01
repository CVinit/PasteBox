package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

type gatedTransferItems struct {
	*TransferStore
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (s *gatedTransferItems) TransferItem(ctx context.Context, transferID, itemID string) (app.TransferItem, error) {
	item, err := s.TransferStore.TransferItem(ctx, transferID, itemID)
	if s.armed.CompareAndSwap(true, false) {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return item, ctx.Err()
		}
	}
	return item, err
}

type uploadObjects struct {
	mu       sync.Mutex
	data     map[string][]byte
	afterPut func()
}

func (o *uploadObjects) PutObject(_ context.Context, key string, data []byte, _ string) error {
	o.mu.Lock()
	o.data[key] = append([]byte(nil), data...)
	o.mu.Unlock()
	if o.afterPut != nil {
		o.afterPut()
	}
	return nil
}
func (o *uploadObjects) GetObject(_ context.Context, key string) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data, ok := o.data[key]
	if !ok {
		return nil, app.ErrObjectNotFound
	}
	return append([]byte(nil), data...), nil
}
func (o *uploadObjects) DeleteObject(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.data, key)
	return nil
}

func TestTransferItemUploadAcrossInstances(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different-bytes=%t", different), func(t *testing.T) { testTransferUploadRace(t, different) })
	}
}
func testTransferUploadRace(t *testing.T, different bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, second := pickupTestPools(t, ctx)
	user := seedStatusUser(t, ctx, pool, "upload-race")
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
	objects := &uploadObjects{data: map[string][]byte{}}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	a := &gatedTransferItems{TransferStore: NewTransferStore(pool), entered: entered, release: release}
	b := &gatedTransferItems{TransferStore: NewTransferStore(second), entered: entered, release: release}
	first := newPostgresBackedService(t, ctx, pool, cfg, func(s *app.Stores) { s.Content.Transfers = a; s.Objects = objects })
	other := newPostgresBackedService(t, ctx, second, cfg, func(s *app.Stores) { s.Content.Transfers = b; s.Objects = objects })
	transfer, err := first.CreateTransferWithContext(ctx, user.ID, app.TransferInput{ExpiresInSeconds: 3600, Items: []app.TransferItemInput{{ItemID: "one", FileName: "one.txt", ContentType: "text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	services := []*app.Service{first, other}
	preflights := make([]app.TransferItemUploadPreflight, 2)
	for i, svc := range services {
		preflights[i], err = svc.PreflightTransferItemUploadWithContext(ctx, user.ID, transfer.ID, "one")
		if err != nil {
			t.Fatal(err)
		}
	}
	a.armed.Store(true)
	b.armed.Store(true)
	type result struct {
		attachment app.AttachmentView
		err        error
	}
	results := make(chan result, 2)
	for i, svc := range services {
		content := "same bytes"
		if different && i == 1 {
			content = "other data"
		}
		upload, err := app.PrepareAttachmentUploadWithLimit("one.txt", "text/plain", bytes.NewBufferString(content), preflights[i].MaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer upload.Close()
			_, attachment, err := svc.AddPreparedTransferItemWithContext(ctx, preflights[i], upload)
			results <- result{attachment, err}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("uploads did not reach the race barrier")
		}
	}
	close(release)
	one, two := <-results, <-results
	if different {
		if one.err == nil {
			one, two = two, one
		}
		var conflict *app.Error
		if !errors.As(one.err, &conflict) || conflict.Code != "transfer_item_conflict" || two.err != nil {
			t.Fatalf("expected one conflict and one winner: %v / %v", one.err, two.err)
		}
	} else if one.err != nil || two.err != nil {
		t.Fatalf("uploads failed: %v / %v", one.err, two.err)
	}
	if !different && one.attachment.ID != two.attachment.ID {
		t.Fatalf("same item created two attachments: %s / %s", one.attachment.ID, two.attachment.ID)
	}
	attachments, err := NewAttachmentStore(pool).ListAttachmentsByPaste(ctx, transfer.PasteID)
	if err != nil || len(attachments) != 1 {
		t.Fatalf("leaked attachments: %d, %v", len(attachments), err)
	}
	ref, err := NewAttachmentStore(pool).ObjectRef(ctx, attachments[0].ObjectKey)
	if err != nil || ref.RefCount != 1 {
		t.Fatalf("leaked refs: %#v %v", ref, err)
	}
	bytes, err := NewDailyMetricStore(pool).DailyMetric(ctx, user.ID, "upload", time.Now())
	if err != nil || bytes != int64(len("same bytes")) {
		t.Fatalf("duplicate quota charge: %d %v", bytes, err)
	}
	objects.mu.Lock()
	objectCount := len(objects.data)
	objects.mu.Unlock()
	if objectCount != 1 {
		t.Fatalf("leaked object bytes: %d", objectCount)
	}
	data, err := objects.GetObject(ctx, attachments[0].ObjectKey)
	if err != nil || (string(data) != "same bytes" && string(data) != "other data") {
		t.Fatalf("winner's bytes lost: %q %v", data, err)
	}
}

type duplicateScanJobStore struct {
	*TransferStore
	jobID string
}

func (s duplicateScanJobStore) CommitTransferItemAttachment(ctx context.Context, input app.TransferItemAttachmentInput) (app.TransferItemAttachmentResult, error) {
	input.ScanJob.ID = s.jobID
	return s.TransferStore.CommitTransferItemAttachment(ctx, input)
}

func TestTransferItemUploadRollsBackAndSupportsGuests(t *testing.T) {
	for _, scenario := range []string{"scan-job-failure", "cancel-during-object-write", "guest"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pool, _ := pickupTestPools(t, ctx)
			user := seedStatusUser(t, ctx, pool, "upload-rollback")
			cfg := config.FromEnv()
			cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
			objects := &uploadObjects{data: map[string][]byte{}}
			svc := newPostgresBackedService(t, ctx, pool, cfg, func(s *app.Stores) {
				s.Objects = objects
				if scenario == "scan-job-failure" {
					s.Content.Transfers = duplicateScanJobStore{NewTransferStore(pool), user.ID}
				}
			})
			input := app.TransferInput{ExpiresInSeconds: 3600, Items: []app.TransferItemInput{{ItemID: "one", FileName: "one.txt"}}}
			var transfer app.TransferView
			var preflight app.TransferItemUploadPreflight
			var err error
			if scenario == "guest" {
				var token string
				token, transfer, err = svc.CreateGuestTransferWithContext(ctx, app.GuestCreateTransferInput{ExpiresInSeconds: 3600, Items: input.Items})
				if err != nil {
					t.Fatal(err)
				}
				preflight, err = svc.PreflightGuestTransferItemUpload(ctx, token, transfer.ID, "one", "", "")
			} else {
				transfer, err = svc.CreateTransferWithContext(ctx, user.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				preflight, err = svc.PreflightTransferItemUploadWithContext(ctx, user.ID, transfer.ID, "one")
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "scan-job-failure" {
				now := time.Now().UTC()
				if err := NewJobStore(pool).CreateQueueItem(ctx, app.QueueItem{ID: user.ID, Kind: "scan", TargetID: "fixture", Status: "pending", RunAfter: now, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM jobs WHERE id=$1", user.ID) })
			}
			if scenario == "cancel-during-object-write" {
				objects.afterPut = func() {
					if _, err := svc.CancelTransferWithContext(ctx, user.ID, transfer.ID); err != nil {
						t.Error(err)
					}
				}
			}
			upload, err := app.PrepareAttachmentUploadWithLimit("one.txt", "text/plain", bytes.NewBufferString("payload"), preflight.MaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer upload.Close()
			_, attachment, uploadErr := svc.AddPreparedTransferItemWithContext(ctx, preflight, upload)
			if scenario == "guest" {
				if uploadErr != nil || attachment.ID == "" {
					t.Fatalf("guest upload: %v", uploadErr)
				}
				paste, err := NewPasteStore(pool).PasteByID(ctx, transfer.PasteID)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", paste.UserID) })
				return
			}
			if uploadErr == nil {
				t.Fatal("expected failed upload")
			}
			attachments, err := NewAttachmentStore(pool).ListAttachmentsByPaste(ctx, transfer.PasteID)
			if err != nil || len(attachments) != 0 {
				t.Fatalf("rollback leaked attachments: %d %v", len(attachments), err)
			}
			item, err := NewTransferStore(pool).TransferItem(ctx, transfer.ID, "one")
			if err != nil || item.AttachmentID != "" || item.Status != app.TransferItemPending {
				t.Fatalf("rollback leaked binding: %#v %v", item, err)
			}
			var refs int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM object_refs WHERE object_key LIKE $1", user.ID+"/%").Scan(&refs); err != nil || refs != 0 {
				t.Fatalf("rollback leaked references: %d %v", refs, err)
			}
			charged, err := NewDailyMetricStore(pool).DailyMetric(ctx, user.ID, "upload", time.Now())
			if err != nil || charged != 0 {
				t.Fatalf("rollback charged bytes: %d %v", charged, err)
			}
			if len(objects.data) != 0 {
				t.Fatal("rollback leaked object bytes")
			}
		})
	}
}
