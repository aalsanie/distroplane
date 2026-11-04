#!/usr/bin/env python3
"""Verify the native Maven UX interpreter is measured and exceeds both coverage gates."""
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

report = Path(sys.argv[1])
if not report.is_file():
    raise SystemExit("Maven JaCoCo coverage XML is missing")
root = ET.parse(report).getroot()
matches = root.findall(".//class[@name='io/github/aalsanie/distroplane/maven/CliFeedback']")
if len(matches) != 1:
    raise SystemExit("Maven CLI feedback class was not measured exactly once")
for kind in ("LINE", "BRANCH"):
    counters = matches[0].findall(f"counter[@type='{kind}']")
    if len(counters) != 1:
        raise SystemExit(f"Maven {kind} coverage counter is missing")
    value = counters[0]
    covered, missed = int(value.attrib["covered"]), int(value.attrib["missed"])
    total = covered + missed
    if total == 0 or covered * 100 <= total * 90:
        raise SystemExit(f"Maven {kind} coverage: {covered}/{total}, required >90%")
    print(f"Maven CLI feedback {kind} coverage: {covered}/{total} ({covered*100/total:.2f}%)")
