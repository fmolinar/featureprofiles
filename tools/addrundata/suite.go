package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const featurePrefix = "feature/"

var packageRegexp = regexp.MustCompile(`^package (\w+)`)

func errorf(format string, args ...any) {
	var buf strings.Builder
	fmt.Fprintf(&buf, format, args...)
	buf.WriteRune('\n')
	os.Stderr.WriteString(buf.String())
}

func getNonTestREADMEs(featureprofilesDir, nonTestREADMEsfilePath string) (map[string]bool, error) {
	filePath := filepath.Join(featureprofilesDir, nonTestREADMEsfilePath)
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	nonTestREADMEs := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			nonTestREADMEs[filepath.Join(featureprofilesDir, line)] = true
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return nonTestREADMEs, nil
}

func validPath(testDir string) bool {
	index := strings.Index(testDir, featurePrefix)
	if index == -1 {
		return false
	}
	testDir = testDir[index+len(featurePrefix):]
	dirs := strings.Split(testDir, "/")
	if len(dirs) < 3 || len(dirs) > 4 {
		return false
	}
	return true
}

// testsuite maps from the test package directory to the various rundata extracted from
// it.
type testsuite map[string]*testcase

// read populates the testsuite from a feature root.  Returns a boolean whether the read
// was successful.  Errors are logged.
func (ts testsuite) read(featuredir string) (ok bool) {
	ok = true
	testdirs := map[string]bool{}
	nonTestREADMEs, err := getNonTestREADMEs(filepath.Dir(featuredir), "tools/non_test_readmes.txt")
	if err != nil {
		return !ok
	}

	err = filepath.WalkDir(featuredir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, "README.md") {
			return nil // Ignore anything that's not a README.md, including intermediate directories.
		}
		if nonTestREADMEs[path] {
			return nil
		}
		testdir := filepath.Dir(path)
		if !validPath(testdir) {
			errorf("Test found in a bad path: %s", testdir)
			ok = false
			return nil
		}
		if !isTestKind(testKind(testdir)) {
			relpath, err := filepath.Rel(filepath.Dir(featuredir), path)
			if err != nil {
				relpath = path
			}
			errorf("Test found in a bad path: %s", relpath)
			ok = false
			return nil
		}
		testdirs[testdir] = true
		return nil
	})

	if err != nil {
		errorf("Error traversing feature directory: %s: %v", featuredir, err)
		ok = false
	}

	for testdir := range testdirs {
		tc := ts[testdir]
		if tc == nil {
			tc = &testcase{}
			ts[testdir] = tc
		}
		if err := tc.read(testdir); err != nil {
			reldir, relerr := filepath.Rel(filepath.Dir(featuredir), testdir)
			if relerr != nil {
				reldir = testdir
			}
			errorf("Error reading testdir: %s: %v", reldir, err)
			ok = false
		} else if err := checkGoTestFilePackageName(testdir); err != nil {
			errorf("error checking test.go package name for dir: %s: %v", testdir, err)
			ok = false
		}
	}

	return ok
}

// testKinds list the valid test kinds.
var testKinds = map[string]string{
	"ate_tests": "ATE Test",
	"kne_tests": "KNE Test",
	"otg_tests": "OTG Test",
	"tests":     "Test",
}

func isTestKind(kind string) bool {
	return testKinds[kind] != ""
}

// testKind returns the test kind given a package testdir of this form:
// <feature>/<subfeature>/<testkind>/<testname>
func testKind(testdir string) string {
	kinddir := filepath.Dir(testdir)
	return filepath.Base(kinddir)
}

func toOTG(testdir string) string {
	return strings.Replace(testdir, "/ate_tests/", "/otg_tests/", 1)
}

// check checks all the rundata in the testsuite for error.  Returns a boolean whether the
// check was successful.  Errors are logged.
func (ts testsuite) check(featuredir string) (ok bool) {
	ok = true
	for _, check := range []func() bool{
		ts.checkCases(featuredir),
		ts.checkDuplicate("test plan ID", func(tc *testcase) string {
			if tc.markdown == nil {
				return ""
			}
			return tc.markdown.PlanId
		}),
		ts.checkDuplicate("test UUID", func(tc *testcase) string {
			if tc.existing == nil {
				return ""
			}
			return tc.existing.Uuid
		}),
		ts.checkATEOTG,
	} {
		if !check() {
			ok = false
		}
	}
	return ok
}

// checkCases returns a function that checks each test case individually.
func (ts testsuite) checkCases(featuredir string) func() bool {
	fn := func() (ok bool) {
		ok = true

		for testdir, tc := range ts {
			errs := tc.check()
			if len(errs) == 0 {
				continue
			}
			ok = false
			reldir, err := filepath.Rel(filepath.Dir(featuredir), testdir)
			if err != nil {
				reldir = testdir
			}
			errorf("Found %d errors in %s", len(errs), reldir)
			for _, err := range errs {
				errorf("  - %v", err)
			}
		}

		return ok
	}

	return fn
}

// checkDuplicate returns a function that checks for duplicate assignments except for
// between ATE and OTG tests of the same test.  The keying is determined by keyfn.
func (ts testsuite) checkDuplicate(what string, keyfn func(tc *testcase) string) func() bool {
	fn := func() (ok bool) {
		ok = true
		wants := map[string]string{} // Maps from key to testdir.

		for got, tc := range ts {
			key := keyfn(tc)
			if key == "" {
				errorf("Skipping check for duplicate %s due to missing value: %s", what, got)
				continue
			}

			want, wantok := wants[key]
			if !wantok {
				wants[key] = got
				continue
			}
			if toOTG(got) != toOTG(want) {
				errorf("Duplicate %s found at %s, already used by %s", what, got, want)
				ok = false
			}
		}

		return ok
	}

	return fn
}

// checkATEOTG ensures that ATE and OTG versions of the same test have the same rundata.
func (ts testsuite) checkATEOTG() (ok bool) {
	ok = true

	for testdir, tc := range ts {
		if testKind(testdir) != "ate_tests" {
			continue
		}
		otgtestdir := toOTG(testdir)
		otgtc := ts[otgtestdir]
		if otgtc == nil {
			continue // Okay if OTG test is missing.
		}

		if tc.existing.PlanId != otgtc.existing.PlanId {
			errorf("ATE and OTG tests have different test plan IDs: %s", testdir)
			errorf("  - ATE: %s", tc.existing.PlanId)
			errorf("  - OTG: %s", otgtc.existing.PlanId)
			ok = false
		}

		if tc.existing.Description != otgtc.existing.Description {
			errorf("ATE and OTG tests have different test descriptions: %s", testdir)
			errorf("  - ATE: %s", tc.existing.Description)
			errorf("  - OTG: %s", otgtc.existing.Description)
			ok = false
		}

		if tc.existing.Uuid != otgtc.existing.Uuid {
			errorf("ATE and OTG tests have different UUIDs: %s", testdir)
			errorf("  - ATE: %s", tc.existing.Uuid)
			errorf("  - OTG: %s", otgtc.existing.Uuid)
			ok = false
		}
	}

	return ok
}

// fix populates the fixed rundata for the entire testsuite.
func (ts testsuite) fix() bool {
	ok := true
	for testdir, tc := range ts {
		if err := tc.fix(); err != nil {
			errorf("Could not fix %s: %v", testdir, err)
			ok = false
		}
	}
	if !ok {
		return false
	}

	// Make sure ATE and OTG tests have the same UUID.
	for testdir, tc := range ts {
		if testKind(testdir) != "ate_tests" {
			continue
		}
		otgtestdir := strings.Replace(testdir, "/ate_tests/", "/otg_tests/", 1)
		otgtc := ts[otgtestdir]
		if otgtc == nil {
			continue // Okay if OTG test is missing.
		}
		otgtc.fixed.Uuid = tc.fixed.Uuid
	}

	return true
}

// write updates the tests under a feature root with the rundata.
func (ts testsuite) write(featuredir string) error {
	updated := false
	parentdir := filepath.Dir(featuredir)

	for testdir, tc := range ts {
		switch err := tc.write(testdir); err {
		case errNoop:
		case nil:
			reldir, err := filepath.Rel(parentdir, testdir)
			if err != nil {
				reldir = testdir
			}
			fmt.Fprintf(os.Stderr, "Updated %s\n", reldir)
			updated = true
		default:
			return err
		}
	}

	if !updated {
		return errNoop
	}
	return nil
}

// checkGoTestFilePackageName iterates through _test.go files in a directory.
func checkGoTestFilePackageName(testdir string) error {
	files, err := os.ReadDir(testdir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(testdir, file.Name())
		if err := checkPackageNameInFile(filePath); err != nil {
			return fmt.Errorf("check failed for %s: %w", file.Name(), err)
		}
	}
	return nil
}

// checkPackageNameInFile handles a single file, allowing defer to work as expected.
func checkPackageNameInFile(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	packageName := ""
	for scanner.Scan() {
		match := packageRegexp.FindSubmatch(scanner.Bytes())
		if match != nil {
			packageName = string(match[1])
			break
		}
	}
	if !strings.HasSuffix(packageName, "_test") {
		return fmt.Errorf("test file %s has package name %s, it should end with _test", filePath, packageName)
	}
	return nil
}
