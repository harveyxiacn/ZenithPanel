package updater

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/registry"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewUpdateInfoUsesRegistryDescriptorDigest(t *testing.T) {
	info := newUpdateInfo(
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		registry.DistributionInspect{
			Descriptor: ocispec.Descriptor{
				Digest: digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222"),
			},
		},
	)

	if !info.Available {
		t.Fatalf("expected update to be available")
	}
	if info.CurrentID != "111111111111" {
		t.Fatalf("expected truncated current id, got %q", info.CurrentID)
	}
	if info.LatestID != "222222222222" {
		t.Fatalf("expected truncated latest id, got %q", info.LatestID)
	}
}

func TestNewUpdateInfoNoUpdateWhenDigestsMatch(t *testing.T) {
	same := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	info := newUpdateInfo(
		same,
		registry.DistributionInspect{
			Descriptor: ocispec.Descriptor{
				Digest: digest.Digest(same),
			},
		},
	)
	if info.Available {
		t.Fatalf("expected no update when digests match")
	}
}

func TestBuildHelperContainerConfigUsesPanelImageWithoutApkInstall(t *testing.T) {
	cfg, hostCfg := buildHelperContainerConfig(DefaultImage, "echo swap")

	if cfg.Image != DefaultImage {
		t.Fatalf("expected helper image %q, got %q", DefaultImage, cfg.Image)
	}
	if strings.Contains(strings.Join(cfg.Cmd, " "), "apk add") {
		t.Fatalf("helper command must not install docker-cli at runtime: %#v", cfg.Cmd)
	}
	if len(hostCfg.Binds) != 1 || hostCfg.Binds[0] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("unexpected helper binds: %#v", hostCfg.Binds)
	}
}
