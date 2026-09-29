import assert from "node:assert/strict";
import test from "node:test";
import worker, { decide, MAX_PAYLOAD } from "./index.js";

const validBody =
  '{"project":"trvl","event":"heartbeat","version":"1.2.3","runtime":"linux/amd64/go1.26.4","install_id":"deadbeef"}';

const cases = [
  {
    name: "happy path returns 204",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: validBody,
    status: 204,
  },
  {
    name: "content type with charset is accepted",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json; charset=utf-8",
    body: validBody,
    status: 204,
  },
  {
    name: "wrong method rejected",
    method: "GET",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: validBody,
    status: 405,
  },
  {
    name: "wrong content type rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "text/plain",
    body: validBody,
    status: 415,
  },
  {
    name: "missing content type rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "",
    body: validBody,
    status: 415,
  },
  {
    name: "oversize body rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: `{"project":"trvl","event":"heartbeat","version":"${"x".repeat(MAX_PAYLOAD)}"}`,
    status: 413,
  },
  {
    name: "unknown field rejected as identity leak",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: '{"project":"trvl","event":"heartbeat","version":"1.2.3","ip":"203.0.113.7"}',
    status: 400,
  },
  {
    name: "malformed json rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: '{"project":"trvl",',
    status: 400,
  },
  {
    name: "missing required field rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: '{"version":"1.2.3","runtime":"linux/amd64/go1.26.4"}',
    status: 400,
  },
  {
    name: "non-string field rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: '{"project":"trvl","event":1}',
    status: 400,
  },
  {
    name: "json array rejected",
    method: "POST",
    pathname: "/v1/heartbeat",
    contentType: "application/json",
    body: "[]",
    status: 400,
  },
  {
    name: "trailing slash is not the heartbeat path",
    method: "POST",
    pathname: "/v1/heartbeat/",
    contentType: "application/json",
    body: validBody,
    status: 404,
  },
];

for (const tc of cases) {
  test(tc.name, () => {
    const got = decide(tc.method, tc.pathname, tc.contentType, tc.body);
    assert.equal(got.status, tc.status);
    if (tc.status === 204) {
      assert.deepEqual(got.record, {
        project: "trvl",
        event: "heartbeat",
        version: "1.2.3",
        runtime: "linux/amd64/go1.26.4",
        install_id: "deadbeef",
      });
    } else {
      assert.equal(got.record, undefined);
    }
  });
}

test("field order in the body does not change the stored record", () => {
  const body =
    '{"install_id":"deadbeef","runtime":"linux/amd64/go1.26.4","version":"1.2.3","event":"heartbeat","project":"trvl"}';
  const got = decide("POST", "/v1/heartbeat", "application/json", body);
  assert.equal(got.status, 204);
  assert.equal(got.record.project, "trvl");
  assert.equal(got.record.install_id, "deadbeef");
});

test("a body of exactly 2048 bytes is accepted", () => {
  const prefix = '{"project":"trvl","event":"heartbeat","version":"';
  const suffix = '"}';
  const pad = "y".repeat(MAX_PAYLOAD - prefix.length - suffix.length);
  const body = prefix + pad + suffix;
  assert.equal(Buffer.byteLength(body), MAX_PAYLOAD);
  const got = decide("POST", "/v1/heartbeat", "application/json", body);
  assert.equal(got.status, 204);
  assert.equal(got.record.version, pad);
});

function request(method, url, { contentType = "application/json", body } = {}) {
  const init = { method, headers: {} };
  if (contentType) init.headers["content-type"] = contentType;
  if (body !== undefined) init.body = body;
  return new Request(url, init);
}

test("an accepted request writes five blobs and the install id index", async () => {
  const points = [];
  const env = {
    HEARTBEAT: {
      writeDataPoint(point) {
        points.push(point);
      },
    },
  };
  const res = await worker.fetch(
    request("POST", "https://telemetry.revaluator.ai/v1/heartbeat?x=1", {
      body: validBody,
    }),
    env,
  );
  assert.equal(res.status, 204);
  assert.equal(await res.text(), "");
  assert.equal(points.length, 1);
  assert.deepEqual(points[0], {
    indexes: ["deadbeef"],
    blobs: ["trvl", "heartbeat", "1.2.3", "linux/amd64/go1.26.4", "deadbeef"],
  });
  assert.equal(JSON.stringify(points[0]).includes("203.0.113"), false);
});

test("a rejected request writes nothing", async () => {
  const points = [];
  const env = {
    HEARTBEAT: {
      writeDataPoint(point) {
        points.push(point);
      },
    },
  };
  const res = await worker.fetch(
    request("POST", "https://telemetry.revaluator.ai/v1/heartbeat", {
      body: '{"project":"trvl","event":"heartbeat","ip":"203.0.113.7"}',
    }),
    env,
  );
  assert.equal(res.status, 400);
  assert.equal(points.length, 0);
});

test("an empty install id is stored under the trvl index", async () => {
  const points = [];
  const env = {
    HEARTBEAT: {
      writeDataPoint(point) {
        points.push(point);
      },
    },
  };
  const res = await worker.fetch(
    request("POST", "https://telemetry.revaluator.ai/v1/heartbeat", {
      body: '{"project":"trvl","event":"heartbeat"}',
    }),
    env,
  );
  assert.equal(res.status, 204);
  assert.deepEqual(points[0].indexes, ["trvl"]);
  assert.equal(points[0].blobs[4], "");
});

test("an install id longer than the index limit still stores the id as a blob", async () => {
  const points = [];
  const env = {
    HEARTBEAT: {
      writeDataPoint(point) {
        points.push(point);
      },
    },
  };
  const id = "p".repeat(97);
  const res = await worker.fetch(
    request("POST", "https://telemetry.revaluator.ai/v1/heartbeat", {
      body: JSON.stringify({
        project: "trvl",
        event: "heartbeat",
        install_id: id,
      }),
    }),
    env,
  );
  assert.equal(res.status, 204);
  assert.deepEqual(points[0].indexes, ["trvl"]);
  assert.equal(points[0].blobs[4], id);
});

test("a storage failure returns 500 and does not echo the payload", async () => {
  const env = {
    HEARTBEAT: {
      writeDataPoint() {
        throw new Error("dataset full");
      },
    },
  };
  const res = await worker.fetch(
    request("POST", "https://telemetry.revaluator.ai/v1/heartbeat", {
      body: validBody,
    }),
    env,
  );
  assert.equal(res.status, 500);
  const text = await res.text();
  assert.equal(text.includes("deadbeef"), false);
  assert.equal(text.includes("dataset full"), false);
});
