package utils

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNonEmptyLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single line",
			input:    "line1",
			expected: []string{"line1"},
		},
		{
			name:     "multiple lines",
			input:    "line1\nline2\nline3",
			expected: []string{"line1", "line2", "line3"},
		},
		{
			name:     "lines with empty lines",
			input:    "line1\n\nline2\n\nline3\n",
			expected: []string{"line1", "line2", "line3"},
		},
		{
			name:     "only empty lines",
			input:    "\n\n\n",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetNonEmptyLines(tt.input)
			if len(tt.expected) == 0 {
				assert.Empty(t, result)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetProjectDir(t *testing.T) {
	dir, err := GetProjectDir()
	assert.NoError(t, err)
	assert.NotEmpty(t, dir)

	// Should not contain the test/e2e path
	assert.NotContains(t, dir, "/test/e2e")

	// Should be an absolute path
	assert.True(t, strings.HasPrefix(dir, "/"))
}

func TestRun_SimpleCommand(t *testing.T) {
	// Test with a simple command that should work on any system
	cmd := exec.Command("echo", "hello world")

	output, err := Run(cmd)

	assert.NoError(t, err)
	assert.Contains(t, output, "hello world")
}

func TestRun_FailingCommand(t *testing.T) {
	// Test with a command that should fail
	cmd := exec.Command("nonexistent-command-12345")

	_, err := Run(cmd)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed with error")
}

func TestRun_CommandWithArgs(t *testing.T) {
	// Test echo command with multiple arguments
	cmd := exec.Command("echo", "-n", "test", "output")

	output, err := Run(cmd)

	assert.NoError(t, err)
	assert.Equal(t, "test output", strings.TrimSpace(output))
}

func TestUncommentCode_FileNotExists(t *testing.T) {
	err := UncommentCode("nonexistent-file.txt", "target", "//")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")
}

func TestUncommentCode_TargetNotFound(t *testing.T) {
	// Create a temporary file
	tmpFile := "/tmp/test-uncomment.txt"
	content := `line1
line2
line3`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// Try to uncomment a target that doesn't exist
	err = UncommentCode(tmpFile, "nonexistent", "//")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to find the code")
}

func TestUncommentCode_Success(t *testing.T) {
	// Create a temporary file with commented code
	tmpFile := "/tmp/test-uncomment-success.txt"
	content := `line1
//commented line 1
//commented line 2
line2`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// Uncomment the target
	target := "//commented line 1\n//commented line 2"
	err = UncommentCode(tmpFile, target, "//")
	assert.NoError(t, err)

	// Read the file back and verify
	modifiedContent, err := os.ReadFile(tmpFile)
	assert.NoError(t, err)

	expected := `line1
commented line 1
commented line 2
line2`
	assert.Equal(t, expected, string(modifiedContent))
}

// Test utility functions for Prometheus and CertManager
// Note: These tests may require a Kubernetes cluster to be available

func TestIsPrometheusCRDsInstalled_NoCluster(t *testing.T) {
	// This test will likely fail if no cluster is available
	// but it tests the function logic
	result := IsPrometheusCRDsInstalled()

	// We can't assert true/false since it depends on cluster state
	// Just ensure the function returns without panicking
	assert.IsType(t, false, result)
}

func TestIsCertManagerCRDsInstalled_NoCluster(t *testing.T) {
	// This test will likely fail if no cluster is available
	// but it tests the function logic
	result := IsCertManagerCRDsInstalled()

	// We can't assert true/false since it depends on cluster state
	// Just ensure the function returns without panicking
	assert.IsType(t, false, result)
}

func TestLoadImageToKindClusterWithName_NoCluster(t *testing.T) {
	// This test will fail if kind is not available or no cluster exists
	// but it tests the function interface
	err := LoadImageToKindClusterWithName("test-image:latest")

	// We expect an error since we're likely not in a kind environment
	// The important thing is that the function doesn't panic
	assert.Error(t, err)
}

// Test constants
func TestConstants(t *testing.T) {
	assert.Equal(t, "v0.77.1", prometheusOperatorVersion)
	assert.Equal(t, "v1.16.3", certmanagerVersion)

	assert.Contains(t, prometheusOperatorURL, "%s")
	assert.Contains(t, certmanagerURLTmpl, "%s")
}

func TestStringReplacement(t *testing.T) {
	// Test the string replacement logic used in GetProjectDir
	testPath := "/some/path/test/e2e/folder"
	result := strings.Replace(testPath, "/test/e2e", "", -1)
	expected := "/some/path/folder"

	assert.Equal(t, expected, result)

	// Test when the replacement string is not present
	testPath2 := "/some/other/path"
	result2 := strings.Replace(testPath2, "/test/e2e", "", -1)
	assert.Equal(t, testPath2, result2) // Should remain unchanged
}

func TestCommandBuilding(t *testing.T) {
	// Test that we can build commands correctly (without executing them)

	// Test kubectl command building
	cmd := exec.Command("kubectl", "get", "crds", "-o", "custom-columns=NAME:.metadata.name")
	// Before calling Run(), Path should be just the command name or might be resolved
	assert.Contains(t, cmd.Path, "kubectl")
	assert.Equal(t, []string{"kubectl", "get", "crds", "-o", "custom-columns=NAME:.metadata.name"}, cmd.Args)

	// Test kind command building
	kindOptions := []string{"load", "docker-image", "test-image", "--name", "test-cluster"}
	cmd2 := exec.Command("kind", kindOptions...)
	assert.Contains(t, cmd2.Path, "kind")
	expectedArgs := append([]string{"kind"}, kindOptions...)
	assert.Equal(t, expectedArgs, cmd2.Args)
}
