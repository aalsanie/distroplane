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

    def test_malformed_tree_is_rejected(self):
        with self.assertRaises(ValueError):
            validate({"children": "untrusted"})


if __name__ == "__main__":
    unittest.main()
