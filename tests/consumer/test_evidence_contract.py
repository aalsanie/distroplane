"""Independent evidence v1 contract fixtures for valid states and journal v2 corruption."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
import zlib

from verify_evidence import sha256, verify


TIME = "2026-11-01T00:00:00Z"


def event(sequence, kind, *, plan=None, operation=False, payload=None):
    record = {"schemaVersion": 2, "sequence": sequence, "runId": "run-fixture",
              "type": kind, "observedAt": TIME, "payload": payload or {}}
    if plan is not None:
        record["planId"] = plan
    if operation:
        record.update(operationId="op-fixture", targetId="target-fixture")
    return record


def frame(item):
    payload = json.dumps(item, separators=(",", ":")).encode("utf-8")
    return (b"DPJ1" + len(payload).to_bytes(4, "big") +
            (zlib.crc32(payload) & 0xffffffff).to_bytes(4, "big") + payload)


def bundle_for(events, state="REJECTED", *, completed=True):
    journal = b"".join(frame(item) for item in events)
    bundle = {
        "schemaVersion": "1", "protocolVersion": "1", "observedAt": TIME,
        "planId": "plan-fixture",
        "run": {"id": "run-fixture", "startedAt": TIME, "completed": completed,
                "cancelled": False},
        "release": {"id": "release-fixture", "artifacts": [
            {"name": "app", "digest": sha256(b"artifact"), "size": 8}]},
        "journal": {"reference": "fixture.journal", "digest": sha256(journal),
                    "bytes": len(journal), "events": len(events), "schemaVersion": 2},
        "targets": [{"id": "target-fixture",
                     "provider": {"name": "external", "version": "1.0.0"},
                     "state": state, "operations": [
                         {"id": "op-fixture", "observedAt": TIME,
                          "kind": "publish", "state": state, "attempt": 1}]}],
    }
    if completed:
        bundle["run"]["completedAt"] = TIME
    return bundle, journal


class EvidenceContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "fixture.journal"
        self.events = [
            event(1, "RUN_STARTED", plan="plan-fixture"),
            event(2, "ATTEMPT_STARTED", operation=True, payload={"attempt": 1}),
            event(3, "OPERATION_REJECTED", operation=True, payload={"attempt": 1}),
            event(4, "RUN_COMPLETED"),
        ]
        self.bundle, self.journal = bundle_for(self.events)
        self.path.write_bytes(self.journal)

    def check(self, data=None):
        return verify(json.dumps(data or self.bundle).encode(), journal=self.path,
                      expected_plan="plan-fixture", expected_run="run-fixture")

    def test_rejection_without_provider_evidence_is_valid(self):
        self.check()
        self.assertEqual(self.check()["targets"][0]["state"], "REJECTED")

    def test_all_supported_normalized_states(self):
        for state in ("PLANNED", "READY", "RUNNING", "BLOCKED",
                      "WAITING_EXTERNAL", "FAILED", "CANCELLED"):
            with self.subTest(state=state):
                events = self.events[:2]
                bundle, data = bundle_for(events, state=state, completed=False)
                self.path.write_bytes(data)
                self.check(bundle)

    def test_noop_target_and_mixed_operation_states(self):
        noop_events = [event(1, "RUN_STARTED", plan="plan-fixture"),
                       event(2, "RUN_COMPLETED")]
        noop, data = bundle_for(noop_events, state="PUBLISHED")
        noop["targets"][0]["operations"] = []
        self.path.write_bytes(data)
        self.check(noop)

        mixed_events = [event(1, "RUN_STARTED", plan="plan-fixture")]
        for kind, op_id in [("ATTEMPT_STARTED", "op-1"),
                            ("OPERATION_PUBLISHED", "op-1"),
                            ("ATTEMPT_STARTED", "op-2"),
                            ("OPERATION_WAITING_EXTERNAL", "op-2")]:
            entry = event(len(mixed_events) + 1, kind, operation=True,
                          payload={"attempt": 1})
            entry["operationId"] = op_id
            mixed_events.append(entry)
        mixed, data = bundle_for(mixed_events, state="WAITING_EXTERNAL", completed=False)
        mixed["targets"][0]["reconcileRequired"] = True
        mixed["targets"][0]["operations"] = [
            {"id": "op-1", "state": "PUBLISHED", "kind": "publish",
             "observedAt": TIME, "attempt": 1},
            {"id": "op-2", "state": "WAITING_EXTERNAL", "kind": "publish",
             "observedAt": TIME, "attempt": 1, "reconcileRequired": True},
        ]
        self.path.write_bytes(data)
        self.check(mixed)

    def test_durable_journal_event_count_and_identity_rejected(self):
        mutated = copy.deepcopy(self.bundle)
        mutated["journal"]["events"] = 3
        with self.assertRaisesRegex(ValueError, "count mismatch"):
            self.check(mutated)
        for field, value in (("planId", "plan-other"), ("runId", "run-other")):
            with self.subTest(field=field):
                data = copy.deepcopy(self.events)
                data[0][field] = value
                self.path.write_bytes(b"".join(frame(item) for item in data))
                changed = copy.deepcopy(self.bundle)
                changed["journal"]["digest"] = sha256(self.path.read_bytes())
                changed["journal"]["bytes"] = self.path.stat().st_size
                with self.assertRaisesRegex(ValueError, "journal.*(plan|run)|bound"):
                    self.check(changed)
        self.path.write_bytes(self.journal)

    def test_crc_sequence_framing_and_trailing_bytes(self):
        modified = bytearray(self.journal)
        modified[24] ^= 1
        self.path.write_bytes(modified)
        changed = copy.deepcopy(self.bundle)
        changed["journal"]["digest"] = sha256(modified)
        with self.assertRaisesRegex(ValueError, "CRC mismatch"):
            self.check(changed)
        altered = copy.deepcopy(self.events)
        altered[1]["sequence"] = 5
        bytes_seq = b"".join(frame(item) for item in altered)
        self.path.write_bytes(bytes_seq)
        changed["journal"]["digest"] = sha256(bytes_seq)
        changed["journal"]["bytes"] = len(bytes_seq)
        with self.assertRaisesRegex(ValueError, "sequence mismatch"):
            self.check(changed)
        self.path.write_bytes(self.journal + b"unexpected")
        with self.assertRaisesRegex(ValueError, "unreported journal tail"):
            self.check()

    def test_only_incomplete_tail_can_be_claimed(self):
        valid_tail = b"DPJ"
        self.path.write_bytes(self.journal + valid_tail)
        changed = copy.deepcopy(self.bundle)
        changed["journal"]["truncatedTail"] = True
        self.check(changed)
        self.path.write_bytes(self.journal + frame(event(5, "ATTEMPT_STARTED", operation=True,
                                                        payload={"attempt": 2})))
        with self.assertRaisesRegex(ValueError, "complete or invalid frame"):
            self.check(changed)
        self.path.write_bytes(self.journal)
        with self.assertRaisesRegex(ValueError, "tail is absent"):
            self.check(changed)

    def test_claimed_terminal_run_requires_terminal_operations(self):
        changed = copy.deepcopy(self.bundle)
        changed["targets"][0]["state"] = "RUNNING"
        changed["targets"][0]["operations"][0]["state"] = "RUNNING"
        with self.assertRaisesRegex(ValueError, "completed run contains unfinished"):
            self.check(changed)
        changed = copy.deepcopy(self.bundle)
        changed["targets"][0]["state"] = "PUBLISHED"
        with self.assertRaisesRegex(ValueError, "target state disagrees"):
            self.check(changed)


if __name__ == "__main__":
    unittest.main()
