// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ghealth/pkg/config"
)

func creds(tokenLen int) *StoredCredentials {
	return &StoredCredentials{
		AccessToken:  strings.Repeat("a", tokenLen),
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
}

// Concurrent savers must never leave the file unparseable. Token payloads differ
// in length between refreshes, which is what makes an in-place O_TRUNC write
// corruptible: a shorter write only overwrites the front of a longer one and the
// tail survives past the end of the JSON.
func TestSaveCredentialsConcurrentStaysParseable(t *testing.T) {
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())

	// Models what actually happens in the field: a handful of ghealth
	// invocations firing at once, each refreshing an expired token.
	const writers = 6
	const rounds = 8

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				// Lengths deliberately differ per writer and per round: equal
				// payloads would hide the corruption this test is about.
				if err := SaveCredentials(creds(100 + i*7 + r)); err != nil {
					t.Errorf("SaveCredentials: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Read while the writers run. A read can transiently fail to open the file
	// on Windows while a replace is in flight — that is retryable and not what
	// this test is about. What must never happen is opening the file and
	// finding content that does not parse: that is the corruption.
	stop := make(chan struct{})
	readResult := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readResult <- nil
				return
			default:
			}
			// Poll rather than spin: a reader that never lets go of the file
			// keeps Windows from ever completing the replace, which is a
			// property of the test loop, not of the code under test.
			time.Sleep(time.Millisecond)

			data, err := os.ReadFile(config.CredentialsPath())
			if err != nil {
				continue // missing or momentarily locked; both are fine
			}
			var got StoredCredentials
			if err := json.Unmarshal(data, &got); err != nil {
				readResult <- fmt.Errorf("%w (%d bytes: %.80q)", err, len(data), data)
				return
			}
		}
	}()

	wg.Wait()
	close(stop)

	if err := <-readResult; err != nil {
		t.Fatalf("concurrent reader saw a corrupt credentials file: %v", err)
	}

	got, err := LoadCredentials()
	if err != nil {
		t.Fatalf("credentials file is unparseable after concurrent writes: %v", err)
	}
	if got.RefreshToken != "refresh-token" {
		t.Fatalf("refresh token not preserved: got %q", got.RefreshToken)
	}
}

// No temp files may be left behind in the config dir.
func TestSaveCredentialsLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)

	for i := 0; i < 5; i++ {
		if err := SaveCredentials(creds(100 + i)); err != nil {
			t.Fatalf("SaveCredentials: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(config.CredentialsPath()); err != nil {
		t.Fatalf("credentials file missing: %v", err)
	}
}
