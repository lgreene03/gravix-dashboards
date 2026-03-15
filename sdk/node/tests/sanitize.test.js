import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { sanitizePath } from "../dist/sanitize.js";

describe("sanitizePath", () => {
  const cases = [
    {
      name: "already templated",
      input: "/api/v1/users/{id}/orders",
      want: "/api/v1/users/{id}/orders",
    },
    {
      name: "UUID in path",
      input: "/users/550e8400-e29b-41d4-a716-446655440000/orders",
      want: "/users/{id}/orders",
    },
    {
      name: "uppercase UUID",
      input: "/users/550E8400-E29B-41D4-A716-446655440000",
      want: "/users/{id}",
    },
    {
      name: "numeric ID 4 digits",
      input: "/api/v1/products/1234",
      want: "/api/v1/products/{id}",
    },
    {
      name: "numeric ID 4 digits trailing slash",
      input: "/api/v1/products/1234/",
      want: "/api/v1/products/{id}/",
    },
    {
      name: "numeric ID many digits",
      input: "/orders/9876543210/items",
      want: "/orders/{id}/items",
    },
    {
      name: "short numeric not replaced",
      input: "/api/v1/health",
      want: "/api/v1/health",
    },
    {
      name: "three digit ID not replaced",
      input: "/items/123",
      want: "/items/123",
    },
    {
      name: "multiple UUIDs",
      input:
        "/tenants/550e8400-e29b-41d4-a716-446655440000/users/661f9400-f39c-51e5-b827-557766551111",
      want: "/tenants/{id}/users/{id}",
    },
    {
      name: "mixed UUID and numeric ID",
      input: "/users/550e8400-e29b-41d4-a716-446655440000/posts/99999",
      want: "/users/{id}/posts/{id}",
    },
    {
      name: "hex token",
      input: "/sessions/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
      want: "/sessions/{id}",
    },
    {
      name: "no IDs to sanitize",
      input: "/api/v1/health",
      want: "/api/v1/health",
    },
    {
      name: "root path",
      input: "/",
      want: "/",
    },
    {
      name: "empty path",
      input: "",
      want: "",
    },
    {
      name: "path with v1 version number preserved",
      input: "/api/v1/users",
      want: "/api/v1/users",
    },
    {
      name: "path with v2 version number preserved",
      input: "/api/v2/orders",
      want: "/api/v2/orders",
    },
  ];

  for (const { name, input, want } of cases) {
    it(name, () => {
      assert.equal(sanitizePath(input), want);
    });
  }
});
