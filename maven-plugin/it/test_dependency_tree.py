"""Verify that transitive dependency drift fails the Maven release gate."""
import unittest

from check_dependency_tree import REQUIRED, validate


class DependencyTreeTest(unittest.TestCase):
    def graph(self):
        return {
            "groupId": "io.github.aalsanie",
            "artifactId": "distroplane-maven-plugin",
            "children": [
                {"groupId": group, "artifactId": artifact, "version": version}
                for (group, artifact), version in REQUIRED.items()
            ],
        }

    def test_exact_runtime_dependencies_pass(self):
        validate(self.graph())

    def test_missing_transitive_component_is_rejected(self):
        root = self.graph()
        root["children"].pop()
        with self.assertRaises(ValueError):
            validate(root)

    def test_old_component_is_rejected(self):
        root = self.graph()
        root["children"][0]["version"] = "2.18.3"
        with self.assertRaises(ValueError):
            validate(root)

    def test_conflicting_duplicate_component_is_rejected(self):
        root = self.graph()
        root["children"].append({
            "groupId": "com.fasterxml.jackson.core",
            "artifactId": "jackson-core",
            "version": "2.17.3",
        })
        with self.assertRaises(ValueError):
            validate(root)

    def test_clean_runtime_vulnerability_scan(self):
        from check_dependency_tree import runtime_dependencies, check_vulnerabilities
        graph = self.graph()
        packages = runtime_dependencies(graph)
        self.assertEqual(len(packages), len(REQUIRED))
        check_vulnerabilities(packages, {"results": [{} for _ in packages]})

    def test_detects_known_vulnerabilities(self):
        from check_dependency_tree import runtime_dependencies, check_vulnerabilities
        packages = runtime_dependencies(self.graph())
        response = {"results": [{"vulns": [{"id": "GHSA-test"}]}] + [{} for _ in packages[1:]]}
        with self.assertRaisesRegex(ValueError, "GHSA-test"):
            check_vulnerabilities(packages, response)

    def test_incomplete_or_paginated_osv_response_is_rejected(self):
        from check_dependency_tree import runtime_dependencies, check_vulnerabilities
        packages = runtime_dependencies(self.graph())
        for response in ({"results": []}, {"results": [{"next_page_token": "more"}] + [{} for _ in packages[1:]]}):
            with self.assertRaises(ValueError):
                check_vulnerabilities(packages, response)

    def test_provided_test_dependencies_are_excluded(self):
        from check_dependency_tree import runtime_dependencies
        graph = self.graph()
        graph["children"].extend([
            {"groupId": "test", "artifactId": "only", "version": "1", "scope": "test"},
            {"groupId": "provided", "artifactId": "only", "version": "1", "scope": "provided"},
        ])
        self.assertEqual(len(runtime_dependencies(graph)), len(REQUIRED))

    def test_malformed_tree_is_rejected(self):
        with self.assertRaises(ValueError):
            validate({"children": "untrusted"})


if __name__ == "__main__":
    unittest.main()
