import { strict as assert } from "node:assert";
import { test } from "node:test";
import { CYCLE, HOUSE_Z, INBOUND_CHECKPOINT_Z, INBOUND_STOP_Z, OUTBOUND_CHECKPOINT_Z, OUTBOUND_STOP_Z, routeAt } from "./timeline.ts";

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

test("the inbound bomb bursts only beside the unprotected house", () => {
  assert.equal(routeAt(12, false).bomb, "truck");
  assert.equal(routeAt(12, true).bomb, "truck");
  assert.equal(routeAt(15, true).bomb, "handoff");
  assert.equal(routeAt(17, true).bomb, "booth");
  assert.equal(routeAt(17, false).bomb, "exploding");
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
    assert.notEqual(protectedState.bomb, "exploding");
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
  assert.equal(routeAt(16.8, false).bomb, "exploding");
  assert.equal(routeAt(17.79, false).bomb, "exploding");
  assert.equal(routeAt(17.8, false).bomb, "hidden");
  assert.equal(routeAt(19.99, false).bomb, "hidden");
});

test("both trips move left to right and use opposite viewport edges", () => {
  const exitZ = 25;
  assert.ok(routeAt(7.29, false, exitZ).z < -exitZ + .1);
  assert.equal(routeAt(10.6, false, exitZ).z, exitZ);
  assert.equal(routeAt(10.6, true, exitZ).z, exitZ);
  for (const protectedRoute of [false, true]) {
    for (const [start, end] of [[2.1, 7.3], [10.6, 18.7]]) {
      for (let t = start; t < end - .1; t += .1) {
        assert.ok(routeAt(t + .1, protectedRoute, exitZ).z <= routeAt(t, protectedRoute, exitZ).z + 1e-8);
      }
    }
  }
});

test("the protected pickup stops on opposite sides of the barrier for each pass", () => {
  assert.equal(routeAt(3.8, true).z, OUTBOUND_STOP_Z);
  assert.equal(routeAt(13.6, true).z, INBOUND_STOP_Z);
  // The truck's nose extends about 2 units ahead of its center.
  assert.ok(OUTBOUND_STOP_Z - 2 > OUTBOUND_CHECKPOINT_Z);
  assert.ok(INBOUND_STOP_Z - 2 > INBOUND_CHECKPOINT_Z);
  assert.equal(routeAt(5, true).guardReach, 1);
  assert.equal(routeAt(5, true).guardWalking, false);
  assert.equal(routeAt(14, true).guardWalking, true);
});

test("barriers stay raised until the pickup's rear clears each checkpoint", () => {
  for (const [checkpoint, start, end] of [
    [OUTBOUND_CHECKPOINT_Z, 6.9, 9.4],
    [INBOUND_CHECKPOINT_Z, 16.8, 18.7],
  ]) {
    let cleared = false;
    for (let time = start; time < end; time += .01) {
      const state = routeAt(time, true);
      if (state.z + 2.2 < checkpoint) cleared = true;
      assert.equal(state.barrierRaised, !cleared, `time=${time}`);
    }
    assert.ok(cleared, `the pickup clears checkpoint ${checkpoint}`);
  }
});

test("both pickups load and stop centered on their house", () => {
  for (const protectedRoute of [false, true]) {
    assert.equal(routeAt(0, protectedRoute).z, HOUSE_Z);
    assert.equal(routeAt(2.1, protectedRoute).z, HOUSE_Z);
    assert.equal(routeAt(protectedRoute ? 18.7 : 16.8, protectedRoute).z, HOUSE_Z);
  }
});

test("seized items reach the guard before disappearing", () => {
  assert.equal(routeAt(5.25, true).privateBox, "handoff");
  assert.equal(routeAt(5.59, true).privateBox, "handoff");
  assert.equal(routeAt(5.6, true).privateBox, "booth");
  assert.equal(routeAt(15.1, true).bomb, "handoff");
  assert.equal(routeAt(15.49, true).bomb, "handoff");
  assert.equal(routeAt(15.5, true).bomb, "booth");
});
