#!/usr/bin/env python3
"""Normalize a `go test -json` stream into stable failure signatures.

Usage:
    normalize_failures.py <stream.json> <stderr.txt>   # signatures to stdout
    normalize_failures.py --self-test                  # run built-in fixtures

Signature forms (sorted, unique, one per line):

    <package>::<TestName>   one per "Action":"fail" event that names a test
    <package>::<build>      only when a failing package produced no failed test
                            event, or the stderr file carries build/setup
                            evidence for that package (R3-10: Go emits a
                            package-level fail event after ordinary test
                            failures too, so a bare package fail event is not
                            build evidence).

Subtest events are kept verbatim ("Parent/child"), so a regression that adds a
single subtest failure shows up as a new signature.
"""

import json
import re
import sys

# `# example.com/pkg` and `# example.com/pkg [example.com/pkg.test]` headers, and
# `FAIL\texample.com/pkg [build failed]` / `[setup failed]` lines.
_STDERR_HEADER = re.compile(r"^# (\S+)")
_STDERR_FAIL = re.compile(r"^FAIL\s+(\S+)\s+\[(?:build|setup) failed\]")


def build_evidence_packages(stderr_text):
    """Packages the stderr stream shows as failing to build or set up."""
    packages = set()
    for line in stderr_text.splitlines():
        header = _STDERR_HEADER.match(line)
        if header:
            packages.add(header.group(1))
            continue
        failed = _STDERR_FAIL.match(line)
        if failed:
            packages.add(failed.group(1))
    return packages


def _strip_test_binary_suffix(import_path):
    """`example.com/pkg [example.com/pkg.test]` -> `example.com/pkg`."""
    return import_path.split(" ", 1)[0]


def signatures(stream_text, stderr_text):
    """Return sorted unique failure signatures for one test run."""
    failed_tests = set()          # (package, test)
    failed_packages = set()       # package with a package-level fail event
    packages_with_failed_test = set()
    build_failed = set(build_evidence_packages(stderr_text))

    for line in stream_text.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue

        action = event.get("Action")
        if action == "build-fail":
            import_path = event.get("ImportPath")
            if import_path:
                build_failed.add(_strip_test_binary_suffix(import_path))
            continue
        if action != "fail":
            continue

        package = event.get("Package")
        if not package:
            continue
        test = event.get("Test")
        if test:
            failed_tests.add((package, test))
            packages_with_failed_test.add(package)
        else:
            failed_packages.add(package)

    result = {"%s::%s" % (pkg, test) for pkg, test in failed_tests}
    for package in failed_packages - packages_with_failed_test:
        result.add("%s::<build>" % package)
    for package in build_failed:
        result.add("%s::<build>" % package)
    return sorted(result)


# --- fixtures ---------------------------------------------------------------

# An ordinary test failure: the package fail event follows a failed test event,
# so it must NOT produce a <build> signature.
_FIXTURE_TEST_FAILURE = """
{"Action":"run","Package":"example.com/p","Test":"TestA"}
{"Action":"output","Package":"example.com/p","Test":"TestA","Output":"    p_test.go:9: boom\\n"}
{"Action":"fail","Package":"example.com/p","Test":"TestA","Elapsed":0}
{"Action":"run","Package":"example.com/p","Test":"TestA/sub"}
{"Action":"fail","Package":"example.com/p","Test":"TestA/sub","Elapsed":0}
{"Action":"run","Package":"example.com/p","Test":"TestB"}
{"Action":"pass","Package":"example.com/p","Test":"TestB","Elapsed":0}
{"Action":"fail","Package":"example.com/p","Elapsed":0.1}
{"Action":"pass","Package":"example.com/q","Elapsed":0.2}
"""

_FIXTURE_TEST_FAILURE_STDERR = ""

# A compile failure: no test events at all, and stderr carries the compiler
# diagnostics.
_FIXTURE_BUILD_FAILURE = """
{"Action":"start","Package":"example.com/r"}
{"Action":"output","Package":"example.com/r","Output":"FAIL\\texample.com/r [build failed]\\n"}
{"Action":"fail","Package":"example.com/r","Elapsed":0}
"""

_FIXTURE_BUILD_FAILURE_STDERR = (
    "# example.com/r\n"
    "r.go:12:2: undefined: nope\n"
    "FAIL\texample.com/r [build failed]\n"
)


def self_test():
    cases = [
        (
            "test failure",
            _FIXTURE_TEST_FAILURE,
            _FIXTURE_TEST_FAILURE_STDERR,
            ["example.com/p::TestA", "example.com/p::TestA/sub"],
        ),
        (
            "build failure",
            _FIXTURE_BUILD_FAILURE,
            _FIXTURE_BUILD_FAILURE_STDERR,
            ["example.com/r::<build>"],
        ),
    ]
    failures = 0
    for name, stream, stderr_text, want in cases:
        got = signatures(stream, stderr_text)
        if got != want:
            failures += 1
            print("FAIL %s:\n  want %s\n  got  %s" % (name, want, got))
        else:
            print("ok   %s -> %s" % (name, got))

    # The two fixtures must produce disjoint signature kinds.
    test_sigs = set(signatures(_FIXTURE_TEST_FAILURE, _FIXTURE_TEST_FAILURE_STDERR))
    build_sigs = set(signatures(_FIXTURE_BUILD_FAILURE, _FIXTURE_BUILD_FAILURE_STDERR))
    if any(sig.endswith("::<build>") for sig in test_sigs):
        failures += 1
        print("FAIL test-failure fixture produced a <build> signature")
    if not all(sig.endswith("::<build>") for sig in build_sigs):
        failures += 1
        print("FAIL build-failure fixture produced a test signature")
    if test_sigs & build_sigs:
        failures += 1
        print("FAIL fixtures share signatures: %s" % sorted(test_sigs & build_sigs))

    if failures:
        print("%d self-test failure(s)" % failures)
        return 1
    print("self-test OK")
    return 0


def main(argv):
    if len(argv) == 2 and argv[1] == "--self-test":
        return self_test()
    if len(argv) != 3:
        print(__doc__.strip(), file=sys.stderr)
        return 2

    with open(argv[1], "r", encoding="utf-8", errors="replace") as stream_file:
        stream_text = stream_file.read()
    with open(argv[2], "r", encoding="utf-8", errors="replace") as stderr_file:
        stderr_text = stderr_file.read()

    for signature in signatures(stream_text, stderr_text):
        print(signature)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
