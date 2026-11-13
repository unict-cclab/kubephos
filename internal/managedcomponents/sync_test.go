package managedcomponents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncMissingImagesAndPrivateTemporaryFiles(t *testing.T) {
	var endpoint Endpoint
	endpoint.Kind = "RegistryEndpoint"
	endpoint.Spec.Host = "registry.test"
	endpoint.Spec.CABundle = "-----BEGIN CERTIFICATE-----"
	var access Credential
	access.Kind = "RegistryCredential"
	access.Spec.Server = endpoint.Spec.Host
	access.Spec.Username = "robot"
	access.Spec.Password = "private-test-secret"
	access.Spec.Project = Project
	for _, failure := range []string{"", "copy", "digest", "permission"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			for _, component := range Catalog() {
				if err := os.WriteFile(filepath.Join(dir, component.Name+"-"+component.Version+".tar"), []byte("archive"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			published := map[string]bool{}
			copies := 0
			temp := ""
			manifest := []byte("{}")
			hash := sha256.Sum256(manifest)
			syncer := Syncer{ArchiveDir: dir, Run: func(_ context.Context, args ...string) ([]byte, error) {
				for _, arg := range args {
					if strings.Contains(arg, access.Spec.Password) {
						t.Fatal("credential exposed in arguments")
					}
				}
				image := args[len(args)-1]
				if args[0] == "inspect" {
					temp = filepath.Dir(args[3])
					info, err := os.Stat(args[3])
					if err != nil || info.Mode().Perm() != 0600 {
						t.Fatal("auth file is not private", err)
					}
					if published[image] {
						return manifest, nil
					}
					if failure == "permission" {
						return []byte("unauthorized"), errors.New("exit status 1")
					}
					return []byte("manifest unknown"), errors.New("exit status 1")
				}
				copies++
				if failure == "copy" {
					return nil, errors.New("exit status 1")
				}
				if !strings.Contains(strings.Join(args, " "), "docker-archive:"+dir) {
					t.Fatal("bundled archive not used", args)
				}
				digest := "sha256:" + hex.EncodeToString(hash[:])
				if failure == "digest" {
					digest = "sha256:wrong"
				}
				for i, arg := range args {
					if arg == "--digestfile" {
						if err := os.WriteFile(args[i+1], []byte(digest), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				published[image] = true
				return nil, nil
			}}
			err := syncer.Ensure(context.Background(), endpoint, access, nil)
			if (err != nil) != (failure != "") {
				t.Fatalf("unexpected result: %v", err)
			}
			if failure == "" && copies != len(Catalog()) {
				t.Fatal("missing images were not all published")
			}
			if failure == "permission" && copies != 0 {
				t.Fatal("authentication failure treated as missing image")
			}
			if temp != "" {
				if _, err := os.Stat(temp); !os.IsNotExist(err) {
					t.Fatal("registry credential files leaked")
				}
			}
		})
	}
}
func TestRegistryProjectCompatibility(t *testing.T) {
	if Image("harbor", "app", "v1") != "harbor/kubephos/app:v1" {
		t.Fatal("neutral project missing")
	}
	if Image("harbor", "app", "v1", "") != "harbor/kubephos-dev/app:v1" {
		t.Fatal("legacy path changed")
	}
	if !ValidProject(Project) || !ValidProject(LegacyProject) || ValidProject("../other") {
		t.Fatal("invalid project validation")
	}
}
