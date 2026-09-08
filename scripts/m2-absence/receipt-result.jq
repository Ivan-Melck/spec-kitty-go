def names($action):
  [.[] | select(.Test != null and .Action == $action) | .Test] | sort;

def duplicates($names):
  $names | group_by(.) | map(select(length > 1) | .[0]);

($expected_text | split("\n") | map(select(length > 0)) | sort) as $expected |
names("run") as $run |
names("pass") as $passed |
names("skip") as $skipped |
names("fail") as $failed |
(($run + $passed + $skipped + $failed) | map(select(. as $name | $expected | index($name) | not)) | unique) as $unmatched |
($expected - $run) as $missing_run |
($expected - $passed) as $missing_pass |
((duplicates($run) + duplicates($passed) + duplicates($skipped) + duplicates($failed)) | unique) as $duplicates |
{
  schema_version: 1,
  kind: $receipt_kind,
  expected: $expected,
  run: $run,
  passed: $passed,
  skipped: $skipped,
  failed: $failed,
  unmatched: $unmatched,
  missing_run: $missing_run,
  missing_pass: $missing_pass,
  duplicates: $duplicates,
  raw_events_sha256: $raw_sha,
  counts: {
    expected: ($expected | length),
    run: ($run | length),
    passed: ($passed | length),
    skipped: ($skipped | length),
    failed: ($failed | length),
    unmatched: ($unmatched | length),
    missing: (($missing_run + $missing_pass) | unique | length),
    duplicates: ($duplicates | length)
  },
  verdict: (if
    ($expected | length) > 0 and ($expected | unique) == $expected and
    $run == $expected and $passed == $expected and
    ($skipped | length) == 0 and ($failed | length) == 0 and
    ($unmatched | length) == 0 and ($missing_run | length) == 0 and
    ($missing_pass | length) == 0 and ($duplicates | length) == 0
  then "pass" else "fail" end)
}
