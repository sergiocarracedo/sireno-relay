// World-space routes run from the warehouse (z = -4.3) towards the exit (z = 6.9).
export const CYCLE = 20;

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
  exclamation: boolean;
};

const mix = (a: number, b: number, t: number) => a + (b - a) * Math.max(0, Math.min(1, t));
const between = (time: number, start: number, end: number) => (time - start) / (end - start);

export function routeAt(elapsed: number, protectedRoute: boolean): RouteState {
  const remainder = elapsed % CYCLE;
  const t = remainder < 0 ? remainder + CYCLE : remainder;
  const outbound = t < 10;
  const state: RouteState = {
    z: -4.3, visible: true, inbound: !outbound,
    ordinary: outbound ? Math.max(0, Math.min(3, Math.floor((t - .45) / .42) + 1)) : 0,
    privateBox: t < 1.2 ? "warehouse" : "truck", bomb: "hidden",
    barrierRaised: false, guardReach: 0, exclamation: false,
  };

  if (outbound) {
    if (t < 3.8) state.z = mix(-4.3, -0.2, between(t, 2.1, 3.8));
    else if (protectedRoute && t < 6.1) state.z = -0.2;
    else state.z = mix(protectedRoute ? -0.2 : -0.2, 7.2, between(t, protectedRoute ? 6.1 : 3.8, protectedRoute ? 8.2 : 7.3));
    if (protectedRoute && t >= 3.8) {
      state.barrierRaised = t >= 6.1;
      state.guardReach = t < 6.1 ? Math.sin(Math.PI * Math.min(1, between(t, 4, 6.1))) : 0;
      state.privateBox = t < 4.25 ? "truck" : t < 5.6 ? "handoff" : "booth";
    }
    if (!protectedRoute && t >= 7.3) {
      state.privateBox = "outside";
      state.exclamation = t < 9.6;
    }
    state.visible = t < (protectedRoute ? 8.2 : 7.3);
  } else {
    state.privateBox = protectedRoute ? "booth" : "outside";
    state.ordinary = 0;
    state.visible = t >= 10.6;
    state.z = t < 10.6 ? 7.2 : protectedRoute
      ? t < 13.6 ? mix(7.2, .35, between(t, 10.6, 13.6))
        : t < 16 ? .35 : mix(.35, -4.3, between(t, 16, 18.4))
      : mix(7.2, -4.3, between(t, 10.6, 16.8));
    state.bomb = t < 10.6 ? "outside" : "truck";
    if (protectedRoute && t >= 13.6) {
      state.barrierRaised = t >= 16;
      state.guardReach = t < 16 ? Math.sin(Math.PI * Math.min(1, between(t, 13.7, 16))) : 0;
      state.bomb = t < 14 ? "truck" : t < 15.5 ? "handoff" : "booth";
    }
    if (!protectedRoute && t >= 16.8) state.bomb = "warehouse";
  }
  return state;
}
