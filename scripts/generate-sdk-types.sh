#!/usr/bin/env bash
# Generate TypeScript types from the OpenAPI spec for the Node SDK.
# Usage: ./scripts/generate-sdk-types.sh
#
# This script extracts component schemas from the OpenAPI spec and
# generates TypeScript interfaces. Run after modifying openapi.json.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OPENAPI_FILE="$ROOT_DIR/services/gateway/openapi.json"
OUTPUT_FILE="$ROOT_DIR/sdk/node/src/generated-types.ts"

if [ ! -f "$OPENAPI_FILE" ]; then
    echo "Error: OpenAPI spec not found at $OPENAPI_FILE"
    exit 1
fi

# Check for python3 (used for JSON parsing)
if ! command -v python3 &> /dev/null; then
    echo "Error: python3 is required"
    exit 1
fi

echo "Generating TypeScript types from $OPENAPI_FILE..."

python3 << 'PYTHON' > "$OUTPUT_FILE"
import json
import sys
import os

spec_path = os.environ.get("OPENAPI_FILE", "services/gateway/openapi.json")
with open(spec_path) as f:
    spec = json.load(f)

schemas = spec.get("components", {}).get("schemas", {})

# Map OpenAPI types to TypeScript
def ts_type(schema, indent=0):
    if "$ref" in schema:
        ref = schema["$ref"].split("/")[-1]
        return ref
    t = schema.get("type", "any")
    if t == "string":
        if "enum" in schema:
            return " | ".join(f'"{v}"' for v in schema["enum"])
        return "string"
    if t == "integer" or t == "number":
        return "number"
    if t == "boolean":
        return "boolean"
    if t == "array":
        items = schema.get("items", {})
        return f"{ts_type(items)}[]"
    if t == "object":
        props = schema.get("properties", {})
        if not props:
            additional = schema.get("additionalProperties")
            if additional:
                return f"Record<string, {ts_type(additional)}>"
            return "Record<string, unknown>"
        lines = []
        required = set(schema.get("required", []))
        pad = "  " * (indent + 1)
        for name, prop in props.items():
            desc = prop.get("description", "")
            opt = "" if name in required else "?"
            comment = f"  /** {desc} */\n{pad}" if desc else ""
            lines.append(f"{comment}{name}{opt}: {ts_type(prop, indent + 1)};")
        inner = f"\n{pad}".join(lines)
        return "{\n" + pad + inner + "\n" + "  " * indent + "}"
    return "unknown"

print("// Auto-generated from services/gateway/openapi.json")
print("// Do not edit manually — run scripts/generate-sdk-types.sh")
print(f"// Generated from OpenAPI spec v{spec['info']['version']}")
print()

for name, schema in sorted(schemas.items()):
    desc = schema.get("description", "")
    if desc:
        print(f"/** {desc} */")
    print(f"export interface {name} {ts_type(schema)}")
    print()
PYTHON

echo "Generated $(grep -c 'export interface' "$OUTPUT_FILE") interfaces -> $OUTPUT_FILE"

# Also generate a Python types file
PY_OUTPUT="$ROOT_DIR/sdk/python/gravix/generated_types.py"
echo "Generating Python types from $OPENAPI_FILE..."

python3 << 'PYTHON' > "$PY_OUTPUT"
import json
import os

spec_path = os.environ.get("OPENAPI_FILE", "services/gateway/openapi.json")
with open(spec_path) as f:
    spec = json.load(f)

schemas = spec.get("components", {}).get("schemas", {})

def py_type(schema):
    if "$ref" in schema:
        ref = schema["$ref"].split("/")[-1]
        return f'"{ref}"'
    t = schema.get("type", "Any")
    if t == "string":
        if "enum" in schema:
            return "str"
        return "str"
    if t == "integer":
        return "int"
    if t == "number":
        return "float"
    if t == "boolean":
        return "bool"
    if t == "array":
        items = schema.get("items", {})
        return f"list[{py_type(items)}]"
    if t == "object":
        return "dict[str, Any]"
    return "Any"

print('"""')
print("Auto-generated from services/gateway/openapi.json")
print("Do not edit manually — run scripts/generate-sdk-types.sh")
print(f"Generated from OpenAPI spec v{spec['info']['version']}")
print('"""')
print()
print("from __future__ import annotations")
print("from dataclasses import dataclass, field")
print("from typing import Any")
print()

for name, schema in sorted(schemas.items()):
    desc = schema.get("description", "")
    props = schema.get("properties", {})
    required = set(schema.get("required", []))

    print()
    print("@dataclass")
    print(f"class {name}:")
    if desc:
        print(f'    """{desc}"""')
    if not props:
        print("    pass")
        continue

    # Required fields first, then optional
    req_fields = [(n, p) for n, p in props.items() if n in required]
    opt_fields = [(n, p) for n, p in props.items() if n not in required]

    for field_name, prop in req_fields:
        fd = prop.get("description", "")
        comment = f"  # {fd}" if fd else ""
        print(f"    {field_name}: {py_type(prop)}{comment}")

    for field_name, prop in opt_fields:
        fd = prop.get("description", "")
        comment = f"  # {fd}" if fd else ""
        default = "None"
        pt = py_type(prop)
        print(f"    {field_name}: {pt} | None = {default}{comment}")

print()
PYTHON

echo "Generated $(grep -c 'class ' "$PY_OUTPUT") classes -> $PY_OUTPUT"
