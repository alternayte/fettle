//go:build contract

package objects_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/alternayte/fettle/internal/objects"
)

// conformance runs the object store suite on one store (REQ-OPS-004).
func conformance(t *testing.T, st objects.Store) {
	ctx := context.Background()
	if err := st.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := st.Get(ctx, "missing/key"); !errors.Is(err, objects.ErrNotFound) {
		t.Fatalf("a missing key gave %v, want ErrNotFound", err)
	}
	big := make([]byte, 40<<20) // over two upload parts, from a reader of unknown size
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "workspaces/w1/1", io.MultiReader(bytes.NewReader(big)), -1); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := st.Put(ctx, "payloads/r1/7", bytes.NewReader([]byte("small")), 5); err != nil {
		t.Fatal(err)
	}
	rc, err := st.Get(ctx, "workspaces/w1/1")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, big) {
		t.Fatalf("read back %d bytes that differ from the %d written", len(got), len(big))
	}
	if err := st.Put(ctx, "payloads/r1/7", bytes.NewReader([]byte("newer")), 5); err != nil {
		t.Fatal(err)
	}
	rc, _ = st.Get(ctx, "payloads/r1/7")
	got, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "newer" {
		t.Fatalf("an overwrite reads %q", got)
	}
	keys, err := st.List(ctx, "workspaces/")
	if err != nil || !slices.Equal(keys, []string{"workspaces/w1/1"}) {
		t.Fatalf("list %v %v", keys, err)
	}
	if err := st.Delete(ctx, "workspaces/w1/1"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "workspaces/w1/1"); err != nil {
		t.Fatalf("a second delete: %v", err)
	}
	if _, err := st.Get(ctx, "workspaces/w1/1"); !errors.Is(err, objects.ErrNotFound) {
		t.Fatalf("a deleted key gave %v", err)
	}
}

func TestSCN_OPS_004_ObjectStoreConformance(t *testing.T) {
	t.Run("disk", func(t *testing.T) {
		st, err := objects.NewDisk(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		conformance(t, st)
	})
	t.Run("minio", func(t *testing.T) {
		ctx := context.Background()
		c, err := minio.Run(ctx, "cgr.dev/chainguard/minio:latest", minio.WithUsername("acmeadmin"), minio.WithPassword("acme-minio-secret"))
		if err != nil {
			t.Fatalf("minio: %v", err)
		}
		t.Cleanup(func() { _ = c.Terminate(context.Background()) })
		addr, err := c.ConnectionString(ctx)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := "http://" + addr
		admin := s3.New(s3.Options{Region: "us-east-1", UsePathStyle: true, BaseEndpoint: aws.String(endpoint), Credentials: credentials.NewStaticCredentialsProvider("acmeadmin", "acme-minio-secret", "")})
		if _, err := admin.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("fettle")}); err != nil {
			t.Fatal(err)
		}
		st, err := objects.NewS3(ctx, objects.S3Config{Endpoint: endpoint, Bucket: "fettle", AccessKey: "acmeadmin", SecretKey: "acme-minio-secret", PathStyle: true})
		if err != nil {
			t.Fatal(err)
		}
		conformance(t, st)
	})
}
