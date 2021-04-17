package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest"
)

// outputCatcher is a helper struct enabling to catch stdout and stderr during
// tests
type outputCatcher struct {
	origStdout *os.File
	origStderr *os.File
	outW       *os.File
	outC       chan string
}

// siacCmdSubTest is a helper struct for running siac Cobra commands subtests
// when subtests need command to run and expected output
type siacCmdSubTest struct {
	name               string
	test               siacCmdTestFn
	cmd                *cobra.Command
	cmdStrs            []string
	expectedOutPattern string
}

// siacCmdTestFn is a type of function to pass to siacCmdSubTest
type siacCmdTestFn func(*testing.T, *cobra.Command, []string, string)

// subTest is a helper struct for running subtests when tests can use the same
// test http client
type subTest struct {
	name string
	test func(*testing.T, client.Client)
}

// escapeRegexChars takes string and escapes all special regex characters
func escapeRegexChars(s string) string {
	res := s
	chars := `\+*?^$.[]{}()|/`
	for _, c := range chars {
		res = strings.ReplaceAll(res, string(c), `\`+string(c))
	}
	return res
}

// executeSiacCommand is a pass-through function to execute siac cobra command
func executeSiacCommand(root *cobra.Command, args ...string) (output string, err error) {
	// Recover from expected die() panic, rethrow any not expected panic
	defer func() {
		if rec := recover(); rec != nil {
			// We are recovering from panic
			if err, ok := rec.(error); !ok || err.Error() != errors.New("die panic for testing").Error() {
				// This is not our expected die() panic, rethrow panic
				panic(rec)
			}
		}
	}()
	_, output, err = executeSiacCommandC(root, args...)
	return output, err
}

// executeSiacCommandC executes cobra command
func executeSiacCommandC(root *cobra.Command, args ...string) (c *cobra.Command, output string, err error) {
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)

	c, err = root.ExecuteC()

	return c, buf.String(), err
}

// getRootCmdForSiacCmdsTests creates and initializes a new instance of siac Cobra
// command
func getRootCmdForSiacCmdsTests(dir string) *cobra.Command {
	// create new instance of siac cobra command
	root := initCmds()

	// initialize a siac cobra command
	initClient(root, &verbose, &httpClient, &dir, &alertSuppress)

	return root
}

// newOutputCatcher starts catching stdout and stderr in tests
func newOutputCatcher() (outputCatcher, error) {
	// redirect stdout, stderr
	origStdout := os.Stdout
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		return outputCatcher{}, errors.New("Error opening pipe")
	}
	os.Stdout = w
	os.Stderr = w

	// capture redirected output
	outC := make(chan string)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, r)
		outC <- b.String()
	}()

	c := outputCatcher{
		origStdout: origStdout,
		origStderr: origStderr,
		outW:       w,
		outC:       outC,
	}

	return c, nil
}

// newTestNode creates a new Sia node for a test
func newTestNode(dir string) (*siatest.TestNode, error) {
	n, err := siatest.NewNode(node.AllModules(dir))
	if err != nil {
		return nil, errors.AddContext(err, "Error creating a new test node")
	}
	return n, nil
}

// runSiacCmdSubTests is a helper function to run siac Cobra command subtests
// when subtests need command to run and expected output
func runSiacCmdSubTests(t *testing.T, tests []siacCmdSubTest) error {
	// Run subtests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.test(t, test.cmd, test.cmdStrs, test.expectedOutPattern)
		})
	}
	return nil
}

// runSubTests is a helper function to run the subtests when tests can use the
// same test http client
func runSubTests(t *testing.T, directory string, tests []subTest) error {
	// Create a test node/client for this test group
	n, err := newTestNode(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Run subtests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.test(t, n.Client)
		})
	}
	return nil
}

// siacTestDir creates a temporary Sia testing directory for a cmd/siac test,
// removing any files or directories that previously existed at that location.
// This should only every be called once per test. Otherwise it will delete the
// directory again.
func siacTestDir(testName string) string {
	path := siatest.TestDir("cmd/siac", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}

// testGenericSiacCmd is a helper function to test siac cobra commands
// specified in cmds for expected output regex pattern
func testGenericSiacCmd(t *testing.T, root *cobra.Command, cmds []string, expOutPattern string) {
	// catch stdout and stderr
	c, err := newOutputCatcher()
	if err != nil {
		t.Fatal("Error starting catching stdout/stderr", err)
	}

	// execute command
	cobraOutput, _ := executeSiacCommand(root, cmds...)

	// stop catching stdout/stderr, get catched outputs
	siaOutput, err := c.stop()
	if err != nil {
		t.Fatal("Error stopping catching stdout/stderr", err)
	}

	// check output
	// There are 2 types of output:
	// 1) Output generated by Cobra commands (e.g. when using -h) or Cobra
	//    errors (e.g. unknown cobra commands or flags).
	// 2) Output generated by siac to stdout and to stderr
	var output string

	if cobraOutput != "" {
		output = cobraOutput
	} else if siaOutput != "" {
		output = siaOutput
	} else {
		t.Fatal("There was no output")
	}

	// check regex pattern by increasing rows so it is easier to spot the regex
	// match issues, do not split on regex pattern rows with open regex groups
	regexErr := false
	regexRows := strings.Split(expOutPattern, "\n")
	offsetFromLastOKRow := 0
	for i := 0; i < len(regexRows); i++ {
		// test only first i+1 rows from regex pattern
		expSubPattern := strings.Join(regexRows[0:i+1], "\n")
		// do not split on open regex group "("
		openRegexGroups := strings.Count(expSubPattern, "(") - strings.Count(expSubPattern, `\(`)
		closedRegexGroups := strings.Count(expSubPattern, ")") - strings.Count(expSubPattern, `\)`)
		if openRegexGroups != closedRegexGroups {
			offsetFromLastOKRow++
			continue
		}
		validPattern := regexp.MustCompile(expSubPattern)
		if !validPattern.MatchString(output) {
			t.Logf("Regex pattern didn't match between row %v, and row %v", i+1-offsetFromLastOKRow, i+1)
			t.Logf("Regex pattern part that didn't match:\n%s", strings.Join(regexRows[i-offsetFromLastOKRow:i+1], "\n"))
			regexErr = true
			break
		}
		offsetFromLastOKRow = 0
	}

	if regexErr {
		t.Log("----- Expected output pattern: -----")
		t.Log(expOutPattern)

		t.Log("----- Actual Cobra output: -----")
		t.Log(cobraOutput)

		t.Log("----- Actual Sia output: -----")
		t.Log(siaOutput)

		t.Fatal()
	}
}

// stop stops catching stdout and stderr, catched output is
// returned
func (c outputCatcher) stop() (string, error) {
	// stop Stdout
	err := c.outW.Close()
	if err != nil {
		return "", err
	}
	os.Stdout = c.origStdout
	os.Stderr = c.origStderr
	output := <-c.outC

	return output, nil
}
