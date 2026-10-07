"""RESET payload schema regressions; run with the Contracts CI jsonschema version.

Run: python contracts/connector/tests/test_reset_schema.py -v
This checks JSON shape, not cross-field generation semantics or real OpenStack.
"""

import copy
import json
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator


class ResetPayloadSchemaTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        schema_path = Path(__file__).resolve().parents[1] / "connector.schema.json"
        cls.schema = json.loads(schema_path.read_text(encoding="utf-8"))
        Draft202012Validator.check_schema(cls.schema)
        cls.validator = Draft202012Validator(
            {
                "$schema": cls.schema["$schema"],
                "$defs": cls.schema["$defs"],
                "$ref": "#/$defs/OperationCommandPayload",
            }
        )

    def payload(self, mutation="RESET"):
        snapshot = {
            "providerConnectionId": "provider-1",
            "vms": [
                {
                    "vmKey": "workspace",
                    "role": "WORKSPACE",
                    "instanceIndex": 0,
                    "imageId": "image-1",
                    "flavorId": "flavor-1",
                    "flavorSpec": {"vcpus": 1, "ramMiB": 1024, "diskGiB": 10},
                }
            ],
            "workspaceVmKey": "workspace",
            "internetOutbound": False,
        }
        result = {"mutationType": mutation}
        if mutation in ("PROVISION", "RESET"):
            result["creationSnapshot"] = snapshot
        if mutation == "RESET":
            result["providerResources"] = [
                {
                    "resourceType": "SERVER",
                    "providerId": "server-1",
                    "generation": 1,
                    "logicalName": "workspace",
                }
            ]
        return result

    def assert_valid(self, payload):
        errors = list(self.validator.iter_errors(payload))
        self.assertEqual([], errors, [error.message for error in errors])

    def assert_invalid(self, payload):
        self.assertTrue(list(self.validator.iter_errors(payload)), "invalid payload accepted")

    def test_reset_with_named_resource_is_valid(self):
        self.assert_valid(self.payload())

    def test_reset_missing_logical_name_is_invalid(self):
        payload = self.payload()
        del payload["providerResources"][0]["logicalName"]
        self.assert_invalid(payload)

    def test_reset_empty_logical_name_is_invalid(self):
        payload = self.payload()
        payload["providerResources"][0]["logicalName"] = ""
        self.assert_invalid(payload)

    def test_reset_non_string_logical_names_are_invalid(self):
        for value in (None, 1):
            with self.subTest(logical_name=value):
                payload = self.payload()
                payload["providerResources"][0]["logicalName"] = value
                self.assert_invalid(payload)

    def test_reset_every_resource_requires_non_empty_logical_name(self):
        payload = self.payload()
        second = copy.deepcopy(payload["providerResources"][0])
        second.update(providerId="server-2", logicalName="")
        payload["providerResources"].append(second)
        self.assert_invalid(payload)

    def test_reset_missing_resources_is_invalid(self):
        payload = self.payload()
        del payload["providerResources"]
        self.assert_invalid(payload)

    def test_reset_empty_or_null_resources_are_invalid(self):
        for value in ([], None):
            with self.subTest(resources=value):
                payload = self.payload()
                payload["providerResources"] = value
                self.assert_invalid(payload)

    def test_provision_without_resources_remains_valid(self):
        self.assert_valid(self.payload("PROVISION"))

    def test_provision_empty_resources_remain_valid(self):
        payload = self.payload("PROVISION")
        payload["providerResources"] = []
        self.assert_valid(payload)

    def test_cleanup_empty_resources_remain_valid(self):
        payload = self.payload("CLEANUP")
        payload["providerResources"] = []
        self.assert_valid(payload)

    def test_non_reset_resource_names_remain_optional(self):
        for mutation in ("PROVISION", "CLEANUP"):
            for include_empty_name in (False, True):
                with self.subTest(mutation=mutation, empty_name=include_empty_name):
                    payload = self.payload(mutation)
                    resource = self.payload()["providerResources"][0]
                    if include_empty_name:
                        resource["logicalName"] = ""
                    else:
                        del resource["logicalName"]
                    payload["providerResources"] = [resource]
                    self.assert_valid(payload)


if __name__ == "__main__":
    unittest.main()
