import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { routeAt } from "./timeline";

const palette = {
  earth: 0x4a453a, grass: 0x3d8557, meadow: 0x5da674, dark: 0x143f30,
  road: 0x344d42, stripe: 0xe5dca5, warehouse: 0xb8c6a0, roof: 0x286148,
  truck: 0x5ec2a0, cab: 0x1a7667, windows: 0x163f45, box: 0xb69364,
  boxSide: 0xcaa873, private: 0xe8fb76, black: 0x171e1e,
  danger: 0xf48566, checkpoint: 0x245846,
};

const material = (color: number, emissive = 0) => new THREE.MeshLambertMaterial({ color, emissive, flatShading: true });
const materials = {
  earth: material(palette.earth), grass: material(palette.grass), meadow: material(palette.meadow),
  dark: material(palette.dark), road: material(palette.road), stripe: material(palette.stripe),
  warehouse: material(palette.warehouse), roof: material(palette.roof), truck: material(palette.truck),
  cab: material(palette.cab), windows: material(palette.windows), box: material(palette.box),
  boxSide: material(palette.boxSide), private: material(palette.private, 0x758c16),
  black: material(palette.black), danger: material(palette.danger, 0x51180d),
  checkpoint: material(palette.checkpoint), skin: material(0xe7bb8c),
};

function cuboid(parent: THREE.Object3D, w: number, h: number, d: number, mat: THREE.Material, x: number, y: number, z: number) {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(w, h, d), mat);
  mesh.position.set(x, y, z);
  parent.add(mesh);
  return mesh;
}

function cone(parent: THREE.Object3D, r: number, h: number, sides: number, mat: THREE.Material, x: number, y: number, z: number) {
  const mesh = new THREE.Mesh(new THREE.ConeGeometry(r, h, sides), mat);
  mesh.position.set(x, y, z);
  parent.add(mesh);
  return mesh;
}

function addCountrysideTerrain(scene: THREE.Scene, source: THREE.Object3D) {
  const terrain = new THREE.Group();
  terrain.name = "countryside-grass-tile-grid";
  scene.add(terrain);
  const scale = 1.2;
  const step = 4 * scale;
  // A 25×25 field extends past the taller hero camera crop in every direction.
  for (let row = -12; row <= 12; row++) for (let column = -12; column <= 12; column++) {
    const tile = source.clone(true);
    tile.scale.setScalar(scale);
    tile.position.set(column * step, -.225, row * step);
    tile.rotation.y = ((row * 5 + column * 3 + 8) % 4) * Math.PI / 2;
    terrain.add(tile);
  }
}

function addStraightRoad(parent: THREE.Object3D, source: THREE.Object3D) {
  for (const z of [-5, -1, 3, 7]) {
    const segment = source.clone(true);
    segment.scale.set(.82, 1, 1);
    segment.position.set(0, -.07, z);
    parent.add(segment);
  }
}

function truckModel(parent: THREE.Object3D) {
  const truck = new THREE.Group();
  parent.add(truck);
  cuboid(truck, 1.35, .43, 2.6, materials.truck, 0, .68, 0);
  cuboid(truck, 1.3, .8, .98, materials.cab, 0, 1.22, .78);
  cuboid(truck, 1.08, .44, .08, materials.windows, 0, 1.32, 1.29);
  cuboid(truck, 1.22, .12, 1.36, materials.dark, 0, .96, -.64);
  for (const x of [-.7, .7]) for (const z of [-.87, .87]) {
    const wheel = new THREE.Mesh(new THREE.CylinderGeometry(.29, .29, .17, 8), materials.black);
    wheel.rotation.z = Math.PI / 2;
    wheel.position.set(x, .36, z);
    truck.add(wheel);
  }
  cuboid(truck, .3, .17, .08, materials.stripe, -.43, .8, 1.34);
  cuboid(truck, .3, .17, .08, materials.stripe, .43, .8, 1.34);
  return truck;
}

function boxModel(parent: THREE.Object3D, glowing = false) {
  const parcel = new THREE.Group();
  parent.add(parcel);
  cuboid(parcel, .57, .57, .57, glowing ? materials.private : materials.box, 0, 0, 0);
  cuboid(parcel, .1, .59, .6, glowing ? materials.stripe : materials.boxSide, 0, 0, 0);
  if (glowing) {
    const halo = new THREE.Mesh(new THREE.TorusGeometry(.44, .045, 5, 10), materials.private);
    halo.rotation.x = Math.PI / 2;
    halo.position.y = -.31;
    parcel.add(halo);
  }
  return parcel;
}

function bombModel(parent: THREE.Object3D) {
  const bomb = new THREE.Group();
  parent.add(bomb);
  const sphere = new THREE.Mesh(new THREE.IcosahedronGeometry(.35, 1), materials.black);
  bomb.add(sphere);
  cuboid(bomb, .09, .26, .09, materials.stripe, 0, .42, 0);
  const spark = cone(bomb, .18, .32, 5, materials.private, 0, .63, 0);
  spark.rotation.z = .48;
  return bomb;
}

type Lane = {
  root: THREE.Group;
  truck: THREE.Group;
  ordinary: THREE.Group[];
  privateBox: THREE.Group;
  bomb: THREE.Group;
  barrier?: THREE.Group;
  guard?: THREE.Group;
  exclamation?: THREE.Group;
};

function buildLane(scene: THREE.Scene, position: THREE.Vector3, protectedRoute: boolean, logoUrl: string, roadTile: THREE.Object3D): Lane {
  const root = new THREE.Group();
  root.position.copy(position);
  // Align each local z-axis route to the camera's horizontal screen axis.
  root.rotation.y = -1.036;
  scene.add(root);
  addStraightRoad(root, roadTile);
  cuboid(root, 2.85, 2.25, 2.25, materials.warehouse, 0, 1.12, -6);
  const roof = cone(root, 2.05, 1.42, 4, materials.roof, 0, 2.95, -6);
  roof.rotation.y = Math.PI / 4;
  cuboid(root, 1.33, 1.39, .08, materials.dark, 0, .7, -4.84);
  cuboid(root, 1.7, .12, .5, materials.boxSide, 0, .05, -4.48);
  for (const px of [-1.02, 1.02]) cuboid(root, .1, 1.55, .11, materials.roof, px, 1.2, -4.79);

  const truck = truckModel(root);
  const ordinary = [boxModel(root), boxModel(root), boxModel(root)];
  const privateBox = boxModel(root, true);
  const bomb = bombModel(root);
  const lane: Lane = { root, truck, ordinary, privateBox, bomb };

  if (protectedRoute) {
    cuboid(root, 1.6, 1.85, 1.5, materials.checkpoint, 2.55, .92, .4);
    const boothRoof = cone(root, 1.19, .83, 4, materials.roof, 2.55, 2.22, .4);
    boothRoof.rotation.y = Math.PI / 4;
    cuboid(root, .67, .57, .05, materials.windows, 2.55, 1.26, 1.17);
    const guard = new THREE.Group();
    root.add(guard); guard.position.set(1.23, 0, -.48); guard.scale.setScalar(1.25);
    cone(guard, .34, .89, 6, materials.stripe, 0, .7, 0);
    const face = new THREE.Mesh(new THREE.IcosahedronGeometry(.23, 1), materials.skin);
    face.position.y = 1.32; guard.add(face);
    cuboid(guard, .58, .12, .54, materials.cab, 0, 1.54, .02);
    cuboid(guard, .14, .66, .17, materials.skin, -.37, .9, .13);
    lane.guard = guard;
    const barrier = new THREE.Group();
    barrier.position.set(-1.7, .09, .7);
    root.add(barrier); lane.barrier = barrier;
    cuboid(root, .24, 1.17, .24, materials.stripe, -1.7, .5, .7);
    const arm = cuboid(barrier, 3.25, .15, .15, materials.stripe, 1.62, .98, 0);
    for (let i = 0; i < 7; i++) cuboid(arm, .19, .16, .16, materials.danger, -1.42 + i * .48, 0, 0);
    const pole = cuboid(root, .14, 2.5, .14, materials.dark, -2.4, 1.25, -1.3);
    pole.rotation.z = .03;
    const sign = cuboid(root, 1.6, 1.05, .08, materials.stripe, -2.4, 2.42, -1.3);
    const signFace = cuboid(root, 1.43, .91, .02, materials.dark, -2.4, 2.42, -1.24);
    signFace.rotation.y = sign.rotation.y;
    const texture = new THREE.TextureLoader().load(logoUrl);
    texture.colorSpace = THREE.SRGBColorSpace;
    const logo = new THREE.Mesh(new THREE.PlaneGeometry(.8, .8), new THREE.MeshBasicMaterial({ map: texture, transparent: true, side: THREE.DoubleSide }));
    logo.position.set(-2.4, 2.42, -1.21);
    root.add(logo);
  } else {
    const exclamation = new THREE.Group();
    root.add(exclamation); exclamation.position.set(-1.68, .6, 6.7);
    cuboid(exclamation, .22, .97, .16, materials.danger, 0, .66, 0);
    cuboid(exclamation, .25, .22, .18, materials.danger, 0, -.04, 0);
    lane.exclamation = exclamation;
  }
  return lane;
}

function positionCargo(object: THREE.Group, where: string, truckZ: number, x: number, inbound: boolean) {
  object.visible = where !== "outside" && where !== "hidden" && where !== "booth";
  if (where === "truck") object.position.set(x, 1.56, truckZ + (inbound ? .55 : -.5));
  if (where === "warehouse") object.position.set(x, .45, -4.15);
  if (where === "handoff") object.position.set(1.06, 1.62, .35);
}

export async function createHeroScene(canvas: HTMLCanvasElement, logoUrl: string, terrainUrl: string, roadUrl: string) {
  const renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true, powerPreference: "low-power" });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  const scene = new THREE.Scene();
  scene.add(new THREE.HemisphereLight(0xeaffdd, 0x27452f, 2.9));
  const sunlight = new THREE.DirectionalLight(0xffefd6, 2.4);
  sunlight.position.set(-6, 15, 9); scene.add(sunlight);
  const loader = new GLTFLoader();
  const [terrain, road] = await Promise.all([loader.loadAsync(terrainUrl), loader.loadAsync(roadUrl)]);
  addCountrysideTerrain(scene, terrain.scene);
  const unsafe = buildLane(scene, new THREE.Vector3(2.3, 0, 3.9), false, logoUrl, road.scene);
  const safe = buildLane(scene, new THREE.Vector3(-2.3, 0, -3.9), true, logoUrl, road.scene);
  const camera = new THREE.OrthographicCamera(-15, 15, 7, -7, .1, 100);
  camera.position.set(16, 23, 27);
  camera.lookAt(0, .3, 0);
  let width = 0; let height = 0;

  function pose(lane: Lane, elapsed: number, protectedRoute: boolean) {
    const state = routeAt(elapsed, protectedRoute);
    lane.truck.visible = state.visible;
    lane.truck.position.z = state.z;
    lane.truck.rotation.y = state.inbound ? Math.PI : 0;
    lane.ordinary.forEach((parcel, i) => {
      parcel.visible = !state.inbound && state.visible;
      const progress = Math.max(0, Math.min(1, (elapsed % 20 - (.45 + i * .42)) / .38));
      parcel.position.set(
        (i - 1) * (.45 - progress * .07),
        .4 + progress * (.93 + (i === 1 ? .45 : 0)),
        -4.12 + progress * (state.z - .53 + 4.12),
      );
    });
    positionCargo(lane.privateBox, state.privateBox, state.z, .28, state.inbound);
    if (state.privateBox === "truck" && !state.inbound && elapsed % 20 < 2.1) {
      const progress = Math.max(0, Math.min(1, (elapsed % 20 - 1.2) / .9));
      lane.privateBox.position.set(.28, .45 + progress * 1.11, -4.15 - progress * .65);
    }
    if (state.privateBox === "handoff") {
      const progress = Math.max(0, Math.min(1, (elapsed % 20 - 4.25) / 1.35));
      lane.privateBox.position.set(.28 + progress * 2.19, 1.56 - progress * .44, -.7 + progress * 1.92);
    }
    positionCargo(lane.bomb, state.bomb, state.z, -.2, state.inbound);
    // The denied bomb disappears inside the booth, never at the warehouse.
    if (state.bomb === "handoff") {
      const progress = Math.max(0, Math.min(1, (elapsed % 20 - 14) / 1.5));
      lane.bomb.position.set(-.2 + progress * 2.67, 1.56 - progress * .44, .9 + progress * .32);
    }
    if (lane.guard) lane.guard.rotation.z = -.24 * state.guardReach;
    if (lane.barrier) lane.barrier.rotation.z = state.barrierRaised ? 1.25 : 0;
    if (lane.exclamation) lane.exclamation.visible = state.exclamation;
  }

  function render(elapsed: number) {
    const rect = canvas.getBoundingClientRect();
    const nextWidth = Math.max(1, Math.floor(rect.width));
    const nextHeight = Math.max(1, Math.floor(rect.height));
    if (nextWidth !== width || nextHeight !== height) {
      width = nextWidth; height = nextHeight;
      renderer.setSize(width, height, false);
    }
    pose(unsafe, elapsed, false);
    pose(safe, elapsed, true);
    const mobile = width < 680;
    renderer.setScissorTest(mobile);
    if (mobile) {
      const half = height / 2;
      for (const [lane, bottom] of [[unsafe, half], [safe, 0]] as const) {
        unsafe.root.visible = lane === unsafe;
        safe.root.visible = lane === safe;
        renderer.setViewport(0, bottom, width, half);
        renderer.setScissor(0, bottom, width, half);
        const aspect = width / half;
        const span = 16.5;
        camera.left = -span / 2; camera.right = span / 2;
        camera.top = span / (2 * aspect); camera.bottom = -camera.top;
        camera.position.set(lane.root.position.x + 10, 18, 20);
        camera.lookAt(lane.root.position.x, .1, 0);
        camera.updateProjectionMatrix();
        renderer.render(scene, camera);
      }
      unsafe.root.visible = safe.root.visible = true;
    } else {
      renderer.setViewport(0, 0, width, height);
      const aspect = width / height;
      const span = 29;
      camera.left = -span / 2; camera.right = span / 2;
      camera.top = span / (2 * aspect); camera.bottom = -camera.top;
      camera.position.set(16, 23, 27);
      camera.lookAt(0, 8.5, 0);
      camera.updateProjectionMatrix();
      renderer.render(scene, camera);
    }
  }

  function dispose() {
    renderer.setAnimationLoop(null);
    const geometries = new Set<THREE.BufferGeometry>();
    const usedMaterials = new Set<THREE.Material>();
    scene.traverse((object) => {
      if (object instanceof THREE.Mesh) {
        geometries.add(object.geometry);
        (Array.isArray(object.material) ? object.material : [object.material]).forEach((mat) => usedMaterials.add(mat));
      }
    });
    geometries.forEach((geometry) => geometry.dispose());
    usedMaterials.forEach((mat) => {
      if (mat instanceof THREE.MeshBasicMaterial) mat.map?.dispose();
      mat.dispose();
    });
    renderer.dispose();
  }
  return { render, dispose };
}
