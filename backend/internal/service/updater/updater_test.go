package updater

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/registry"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewUpdateInfoUsesRegistryDescriptorDigest(t *testing.T) {
	info := newUpdateInfo(
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		[]string{"ghcr.io/harveyxiacn/zenithpanel@sha256:1111111111111111111111111111111111111111111111111111111111111111"},
		DefaultImage,
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

func TestMergeContainerLabelsUsesCandidateOCIAndPreservesOperatorLabels(t *testing.T) {
	got := mergeContainerLabels(
		map[string]string{
			"operator.example/role":             "panel",
			"org.opencontainers.image.revision": "old-revision",
			"org.opencontainers.image.version":  "old-version",
		},
		map[string]string{
			"org.opencontainers.image.revision": "new-revision",
			"org.opencontainers.image.version":  "new-version",
			"com.example/image-build":           "candidate-only-metadata",
		},
	)
	if got["operator.example/role"] != "panel" {
		t.Fatalf("operator label was not preserved: %#v", got)
	}
	if got["org.opencontainers.image.revision"] != "new-revision" || got["org.opencontainers.image.version"] != "new-version" {
		t.Fatalf("candidate OCI labels were not applied: %#v", got)
	}
	if _, ok := got["com.example/image-build"]; ok {
		t.Fatalf("non-OCI candidate image label should not become a container label: %#v", got)
	}
}

func TestSwapScriptChecksCandidateImageIDBeforeHealth(t *testing.T) {
	s := swapScript()
	idEnv := strings.Index(s, `candidate_image_id="${ZENITH_CANDIDATE_IMAGE_ID:-}"`)
	check := strings.Index(s, `docker inspect --format '{{.Image}}' "$new_id"`)
	health := strings.Index(s, `docker exec "$new_id"`)
	if idEnv == -1 || check == -1 || health == -1 || check > health {
		t.Fatalf("swap script must validate candidate image ID before health: %s", s)
	}
}

func TestValidateExternalRunnerTopology(t *testing.T) {
	withData := func(mode, source string) container.InspectResponse {
		return container.InspectResponse{
			HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(mode)},
			Mounts:     []container.MountPoint{{Type: mount.TypeBind, Source: source, Destination: panelDataPath, RW: true}},
		}
	}
	panel := withData("host", "/opt/zenithpanel/data")
	if err := validateExternalRunnerTopology(panel, withData("host", "/opt/zenithpanel/data")); err != nil {
		t.Fatalf("valid topology: %v", err)
	}
	for _, runner := range []container.InspectResponse{
		withData("bridge", "/opt/zenithpanel/data"),
		withData("host", "/other/data"),
	} {
		if err := validateExternalRunnerTopology(panel, runner); err == nil {
			t.Fatal("accepted unsafe runner topology")
		}
	}
}

func TestValidateExternalRunnerIdentityRejectsUpdateContainerNames(t *testing.T) {
	for _, runnerName := range []string{"panel", "panel-old", "zenith-updater"} {
		if err := validateExternalRunnerIdentity("panel-id", "runner-id", runnerName, "panel"); err == nil {
			t.Fatalf("accepted dangerous runner name %q", runnerName)
		}
	}
	if err := validateExternalRunnerIdentity("same", "same", "runner", "panel"); err == nil {
		t.Fatal("accepted panel as runner")
	}
	if err := validateExternalRunnerIdentity("panel-id", "runner-id", "zenith-proxy", "panel"); err != nil {
		t.Fatalf("valid runner rejected: %v", err)
	}
}

func TestNewUpdateInfoNoUpdateWhenDigestsMatch(t *testing.T) {
	same := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	info := newUpdateInfo(
		"sha256:configconfigconfigconfigconfigconfigconfigconfigconfigconfig00",
		[]string{"ghcr.io/harveyxiacn/zenithpanel@" + same},
		DefaultImage,
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

func TestSwapScriptPrunesDanglingImagesAfterRemovingOldContainer(t *testing.T) {
	s := swapScript()
	rm := strings.Index(s, `docker rm "$old_id"`)
	prune := strings.Index(s, "docker image prune -f")
	if rm == -1 || prune == -1 {
		t.Fatalf("swap script missing rm or prune: %s", s)
	}
	if prune < rm {
		t.Fatalf("prune must run after the old container is removed (its image is still referenced before): %s", s)
	}
	if strings.Contains(s, "prune -af") || strings.Contains(s, "prune -a") {
		t.Fatalf("prune must be dangling-only, never -a: %s", s)
	}
}

func TestSwapScriptHealthChecksBeforeRemovalAndRollsBack(t *testing.T) {
	s := swapScript()
	health := strings.Index(s, `docker exec "$new_id"`)
	removeOld := strings.Index(s, `docker rm "$old_id"`)
	if health == -1 || removeOld == -1 || health > removeOld {
		t.Fatalf("health check must precede old-container removal: %s", s)
	}
	for _, required := range []string{`rollback()`, `docker rm -f "$new_id"`, `docker rename "$old_id" "$original_name"`, `docker start "$old_id"`} {
		if !strings.Contains(s, required) {
			t.Fatalf("rollback script missing %q: %s", required, s)
		}
	}
}

func TestBuildHelperContainerConfigUsesPanelImageWithoutApkInstall(t *testing.T) {
	cfg, hostCfg := buildHelperContainerConfig(DefaultImage, "old", "new", "zenithpanel", "old-image", "new-image")

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

func TestVerifyCandidateSignatureUsesConfiguredIdentity(t *testing.T) {
	original := runCosignVerify
	t.Cleanup(func() { runCosignVerify = original })
	t.Setenv("ZENITH_COSIGN_IDENTITY", "workflow-identity")
	t.Setenv("ZENITH_COSIGN_ISSUER", "issuer")
	var gotCandidate, gotIdentity, gotIssuer string
	runCosignVerify = func(_ context.Context, candidate, identity, issuer string) ([]byte, error) {
		gotCandidate, gotIdentity, gotIssuer = candidate, identity, issuer
		return nil, nil
	}
	if err := verifyCandidateSignature(context.Background(), "repo@sha256:abc"); err != nil {
		t.Fatal(err)
	}
	if gotCandidate != "repo@sha256:abc" || gotIdentity != "workflow-identity" || gotIssuer != "issuer" {
		t.Fatalf("verify args = %q %q %q", gotCandidate, gotIdentity, gotIssuer)
	}
}

func TestVerifyCandidateSignatureRejectsFailureAndCanBeExplicitlyDisabled(t *testing.T) {
	original := runCosignVerify
	t.Cleanup(func() { runCosignVerify = original })
	calls := 0
	runCosignVerify = func(_ context.Context, _, _, _ string) ([]byte, error) {
		calls++
		return []byte("invalid signature"), errors.New("exit 1")
	}
	if err := verifyCandidateSignature(context.Background(), "repo@sha256:abc"); err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("expected signature failure, got %v", err)
	}
	t.Setenv("ZENITH_COSIGN_VERIFY", "false")
	if err := verifyCandidateSignature(context.Background(), "repo@sha256:abc"); err != nil {
		t.Fatalf("explicit disable should bypass verification: %v", err)
	}
	if calls != 1 {
		t.Fatalf("verification calls=%d, want 1", calls)
	}
}

func TestImageRepository(t *testing.T) {
	for input, want := range map[string]string{
		"ghcr.io/o/r:main":          "ghcr.io/o/r",
		"registry:5000/o/r:v1":      "registry:5000/o/r",
		"ghcr.io/o/r@sha256:abcdef": "ghcr.io/o/r",
	} {
		if got := imageRepository(input); got != want {
			t.Errorf("imageRepository(%q)=%q, want %q", input, got, want)
		}
	}
}
