// Local z increases toward screen left. Both trips drive toward decreasing z.
// The house is at z = 0, between the return and outbound checkpoints.
export const CYCLE = 20;
export const HOUSE_Z = 0;
export const INBOUND_CHECKPOINT_Z = 5.4;
export const OUTBOUND_CHECKPOINT_Z = -5.4;
// Stop with the nose just short of each barrier, not inside it.
export const INBOUND_STOP_Z = 7.65;
export const OUTBOUND_STOP_Z = -3.05;

export type CargoLocation = "warehouse" | "truck" | "handoff" | "booth" | "outside";
export type RouteState = {
  z: number;
  visible: boolean;
  inbound: boolean;
  ordinary: number;
  privateBox: CargoLocation;
  bomb: CargoLocation | "hidden";
  barrierRaised: boolean;
  guardReach: number;
  guardWalking: boolean;
  exclamation: boolean;
};

const mix = (a: number, b: number, t: number) => a + (b - a) * Math.max(0, Math.min(1, t));
const between = (time: number, start: number, end: number) => (time - start) / (end - start);
const ease = (value: number) => {
  const t = Math.max(0, Math.min(1, value));
  return t * t * (3 - 2 * t);
};

export function routeAt(elapsed: number, protectedRoute: boolean, exitZ = 20): RouteState {
  const remainder = elapsed % CYCLE;
  const t = remainder < 0 ? remainder + CYCLE : remainder;
  const outbound = t < 10;
  const state: RouteState = {
    z: HOUSE_Z, visible: true, inbound: !outbound,
    ordinary: outbound ? Math.max(0, Math.min(3, Math.floor((t - .45) / .42) + 1)) : 0,
    privateBox: t < 1.2 ? "warehouse" : "truck", bomb: "hidden",
    barrierRaised: false, guardReach: 0, guardWalking: false, exclamation: false,
  };

  if (outbound) {
    if (t < 3.8) state.z = mix(HOUSE_Z, protectedRoute ? OUTBOUND_STOP_Z : -4.2, between(t, 2.1, 3.8));
    else if (protectedRoute && t < 6.9) state.z = OUTBOUND_STOP_Z;
    else state.z = mix(protectedRoute ? OUTBOUND_STOP_Z : -4.2, -exitZ, between(t, protectedRoute ? 6.9 : 3.8, protectedRoute ? 9.4 : 7.3));
    if (protectedRoute && t >= 3.8) {
      state.barrierRaised = t >= 6.9 && t < 9.4;
      if (t >= 4 && t < 4.9) {
        state.guardReach = ease(between(t, 4, 4.9));
        state.guardWalking = true;
      } else if (t >= 4.9 && t < 7) state.guardReach = 1;
      else if (t >= 7 && t < 7.9) {
        state.guardReach = 1 - ease(between(t, 7, 7.9));
        state.guardWalking = true;
      }
      state.privateBox = t < 4.25 ? "truck" : t < 5.6 ? "handoff" : "booth";
    }
    if (!protectedRoute && t >= 7.3) {
      state.privateBox = "outside";
      state.exclamation = t < 9.6;
    }
    state.visible = t < (protectedRoute ? 9.4 : 7.3);
  } else {
    state.privateBox = protectedRoute ? "booth" : "outside";
    state.ordinary = 0;
    state.visible = t >= 10.6;
    state.z = t < 10.6 ? exitZ : protectedRoute
      ? t < 13.6 ? mix(exitZ, INBOUND_STOP_Z, between(t, 10.6, 13.6))
        : t < 16.8 ? INBOUND_STOP_Z : mix(INBOUND_STOP_Z, HOUSE_Z, between(t, 16.8, 18.7))
      : mix(exitZ, HOUSE_Z, between(t, 10.6, 16.8));
    state.bomb = t < 10.6 ? "outside" : "truck";
    if (protectedRoute && t >= 13.6) {
      state.barrierRaised = t >= 16.8 && t < 18.7;
      if (t >= 13.6 && t < 14.5) {
        state.guardReach = ease(between(t, 13.6, 14.5));
        state.guardWalking = true;
      } else if (t >= 14.5 && t < 16.8) state.guardReach = 1;
      else if (t >= 16.8 && t < 17.7) {
        state.guardReach = 1 - ease(between(t, 16.8, 17.7));
        state.guardWalking = true;
      }
      state.bomb = t < 14 ? "truck" : t < 15.5 ? "handoff" : "booth";
    }
    if (!protectedRoute && t >= 16.8) state.bomb = "warehouse";
  }
  return state;
}
