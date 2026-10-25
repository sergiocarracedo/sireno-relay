import { strict as assert } from "node:assert";
import { test } from "node:test";
import { CYCLE, routeAt } from "./timeline.ts";

test("identical outbound loads diverge only at checkpoint", () => {
  assert.equal(routeAt(2, false).privateBox, "truck");
  assert.equal(routeAt(2, true).privateBox, "truck");
  assert.equal(routeAt(5, false).privateBox, "truck");
  assert.equal(routeAt(5, true).privateBox, "handoff");
  assert.equal(routeAt(6, true).barrierRaised, false);
  assert.equal(routeAt(7, true).privateBox, "booth");
  assert.equal(routeAt(8, false).exclamation, true);
  assert.equal(routeAt(8, false).privateBox, "outside");
});

test("inbound bomb reaches only the unprotected warehouse", () => {
  assert.equal(routeAt(12, false).bomb, "truck");
  assert.equal(routeAt(12, true).bomb, "truck");
  assert.equal(routeAt(15, true).bomb, "handoff");
  assert.equal(routeAt(17, true).bomb, "booth");
  assert.equal(routeAt(17, false).bomb, "warehouse");
  assert.equal(routeAt(17, true).privateBox, "booth");
});

test("all samples are repeatable, finite and loop seamlessly", () => {
  for (let t = 0; t < CYCLE; t += .125) {
    for (const route of [true, false]) {
      assert.deepEqual(routeAt(t, route), routeAt(t + 2 * CYCLE, route));
      assert.ok(Number.isFinite(routeAt(t, route).z));
      assert.ok(Number.isFinite(routeAt(t, route).guardReach));
    }
  }
  assert.deepEqual(routeAt(0, true), routeAt(CYCLE, true));
});

test("the protected lane never delivers restricted cargo or the return bomb", () => {
  for (let t = 0; t < CYCLE; t += .05) {
    const protectedState = routeAt(t, true);
    const directState = routeAt(t, false);
    assert.notEqual(protectedState.privateBox, "outside");
    assert.notEqual(protectedState.bomb, "warehouse");
    assert.notEqual(directState.privateBox, "booth");
    assert.notEqual(directState.bomb, "booth");
    assert.equal(directState.guardReach, 0);
    assert.equal(directState.barrierRaised, false);
    if (protectedState.privateBox === "handoff" || protectedState.bomb === "handoff") {
      assert.equal(protectedState.visible, true);
    }
  }
});

test("each transition has a visible dwell before the next trip", () => {
  assert.equal(routeAt(0, false).ordinary, 0);
  assert.equal(routeAt(.5, false).ordinary, 1);
  assert.equal(routeAt(1, false).ordinary, 2);
  assert.equal(routeAt(2, false).ordinary, 3);
  assert.equal(routeAt(2, true).ordinary, 3);
  for (const [time, expected] of [
    [0, "warehouse"], [1.2, "truck"], [4.24, "truck"],
    [4.25, "handoff"], [5.6, "booth"],
  ] as const) assert.equal(routeAt(time, true).privateBox, expected, `time=${time}`);
  assert.equal(routeAt(13.59, true).bomb, "truck");
  assert.equal(routeAt(14.0, true).bomb, "handoff");
  assert.equal(routeAt(15.5, true).bomb, "booth");
  assert.equal(routeAt(16.8, false).bomb, "warehouse");
  assert.equal(routeAt(19.99, false).bomb, "warehouse");
});
