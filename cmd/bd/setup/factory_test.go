package setup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFactoryDoesNotTouchAgentsFile(t *testing.T) {
	tests := []struct {
		name          string
		action        func()
		agentsContent []byte
		wantOutput    string
		wantAbsent    bool
	}{
		{
			name:       "bd setup factory/no AGENTS.md",
			action:     InstallFactory,
			wantOutput: factoryBeadsSection,
			wantAbsent: true,
		},
		{
			name:          "bd setup factory/plain AGENTS.md",
			action:        InstallFactory,
			agentsContent: []byte("# Project Instructions\nKeep this content.\n"),
			wantOutput:    factoryBeadsSection,
		},
		{
			name:          "bd setup factory/marked AGENTS.md",
			action:        InstallFactory,
			agentsContent: []byte("# Project Instructions\n\n" + factoryBeadsSection + "\nExisting instructions remain.\n"),
			wantOutput:    factoryBeadsSection,
		},
		{
			name:          "bd setup factory --remove/marked-only AGENTS.md",
			action:        RemoveFactory,
			agentsContent: []byte(factoryBeadsSection),
			wantOutput:    factoryBeginMarker,
		},
		{
			name:          "bd setup factory --remove/curated marked AGENTS.md",
			action:        RemoveFactory,
			agentsContent: []byte("# Curated Instructions\n\n" + factoryBeadsSection + "\nKeep this content.\n"),
			wantOutput:    factoryBeginMarker,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			agentsPath := filepath.Join(tmpDir, "AGENTS.md")
			if !tt.wantAbsent {
				if err := os.WriteFile(agentsPath, tt.agentsContent, 0644); err != nil {
					t.Fatalf("write AGENTS.md: %v", err)
				}
			}

			output := captureFactoryOutput(t, tt.action)
			if !strings.Contains(output, tt.wantOutput) {
				t.Errorf("output missing %q: %q", tt.wantOutput, output)
			}

			content, err := os.ReadFile(agentsPath)
			if tt.wantAbsent {
				if err == nil {
					t.Fatalf("AGENTS.md was created: %q", content)
				}
				if !os.IsNotExist(err) {
					t.Fatalf("read AGENTS.md: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("read AGENTS.md: %v", err)
			}
			if !bytes.Equal(content, tt.agentsContent) {
				t.Errorf("AGENTS.md changed:\nwant %q\n got %q", tt.agentsContent, content)
			}
		})
	}
}

func captureFactoryOutput(t *testing.T, action func()) string {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer func() {
		os.Stdout = originalStdout
	}()

	os.Stdout = writer
	action()
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	os.Stdout = originalStdout

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	return string(output)
}
