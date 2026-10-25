import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { DRACOLoader } from "three/addons/loaders/DRACOLoader.js";
import { clone as cloneSkinnedModel } from "three/addons/utils/SkeletonUtils.js";
import { HOUSE_Z, INBOUND_CHECKPOINT_Z, OUTBOUND_CHECKPOINT_Z, routeAt } from "./timeline";

const material = (color: number, emissive = 0) => new THREE.MeshLambertMaterial({ color, emissive, flatShading: true });
const materials = {
  stripe: material(0xe5dca5),
  danger: material(0xf48566, 0x51180d),
};
const ROAD_ANGLE = -Math.atan2(27, 16);
const ROAD_SHIFT = new THREE.Vector2(-1.8, -3);
const ROAD_CENTERS = [[2.3 + ROAD_SHIFT.x, 3.9 + ROAD_SHIFT.y], [-2.3 + ROAD_SHIFT.x, -3.9 + ROAD_SHIFT.y]] as const;
const roadDistance = (x: number, z: number) => Math.min(...ROAD_CENTERS.map(([cx, cz]) =>
  Math.abs(Math.cos(ROAD_ANGLE) * (x - cx) - Math.sin(ROAD_ANGLE) * (z - cz))));

function cuboid(parent: THREE.Object3D, w: number, h: number, d: number, mat: THREE.Material, x: number, y: number, z: number) {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(w, h, d), mat);
  mesh.position.set(x, y, z);
  parent.add(mesh);
  return mesh;
}

function terrainElevation(x: number, z: number) {
  const broadRoll = Math.sin(x * .105) * Math.cos(z * .09) * .9;
  const smallerRoll = Math.sin(x * .21 + z * .14) * .36;
  const leftRise = Math.exp(-((x + 22) ** 2 + (z - 14) ** 2) / 190) * .85;
  const rightRise = Math.exp(-((x - 18) ** 2 + (z + 18) ** 2) / 230) * .65;
  const natural = broadRoll + smallerRoll + leftRise + rightRise;
  const distance = roadDistance(x, z);
  const blend = THREE.MathUtils.smoothstep(distance, 2.25, 4.6);
  return THREE.MathUtils.lerp(.43, natural, blend);
}

function addFacetedGround(scene: THREE.Scene) {
  // Adapted from the elevation-mesh approach: a coarse shared-vertex grid,
  // displaced by elevation then shaded flat with one material.
  const extent = 148;
  const segments = 96;
  const geometry = new THREE.PlaneGeometry(extent, extent, segments, segments);
  geometry.rotateX(-Math.PI / 2);
  const positions = geometry.getAttribute("position");
  const step = extent / segments;
  for (let row = 0; row <= segments; row++) for (let column = 0; column <= segments; column++) {
    const index = row * (segments + 1) + column;
    const isEdge = row === 0 || row === segments || column === 0 || column === segments;
    const jitter = Math.sin(column * 79.17 + row * 37.73) * .5;
    if (!isEdge) {
      positions.setX(index, positions.getX(index) + jitter * step * .34);
      positions.setZ(index, positions.getZ(index) + Math.cos(column * 31.41 + row * 97.19) * step * .34);
    }
    positions.setY(index, terrainElevation(positions.getX(index), positions.getZ(index)));
  }
  const facetedGeometry = geometry.toNonIndexed();
  facetedGeometry.computeVertexNormals();
  const faceColors = new Float32Array(facetedGeometry.getAttribute("position").count * 3);
  const baseGreen = new THREE.Color(0x43845e);
  const shadedGreen = new THREE.Color(0x36714f);
  for (let face = 0; face < faceColors.length / 9; face++) {
    const shadeFace = (face * 37 + Math.floor(face / 7) * 13) % 11 === 0;
    const color = shadeFace ? shadedGreen : baseGreen;
    for (let vertex = 0; vertex < 3; vertex++) color.toArray(faceColors, face * 9 + vertex * 3);
  }
  facetedGeometry.setAttribute("color", new THREE.BufferAttribute(faceColors, 3));
  const ground = new THREE.Mesh(facetedGeometry, new THREE.MeshLambertMaterial({ vertexColors: true, flatShading: true }));
  ground.name = "dark-faceted-ground";
  scene.add(ground);
}

function addScenery(scene: THREE.Scene, models: { rock: THREE.Object3D; spruce: THREE.Object3D; oak: THREE.Object3D }) {
  const screenRight = new THREE.Vector2(.86, -.51);
  const awayFromCamera = new THREE.Vector2(-.51, -.86);
  const terrainPoint = (across: number, depth: number) => new THREE.Vector2(
    screenRight.x * across + awayFromCamera.x * depth,
    screenRight.y * across + awayFromCamera.y * depth,
  );
  const place = (source: THREE.Object3D, x: number, z: number, scale: number, base: number, rotation: number) => {
    // No decoration (including canopies or rock outcrops) may overlap a road.
    if (roadDistance(x, z) < 5.5) return;
    const object = source.clone(true);
    object.scale.setScalar(scale);
    object.position.set(x, terrainElevation(x, z) + base * scale, z);
    object.rotation.y = rotation;
    scene.add(object);
  };
  const rockPositions = [[-16, 8], [-11, 15], [-6, 10], [0, 17], [6, 8], [11, 14], [16, 9]] as const;
  rockPositions.forEach(([across, depth], index) => {
    const point = terrainPoint(across, depth);
    place(models.rock, point.x + ROAD_SHIFT.x, point.y + ROAD_SHIFT.y, .52 + (index % 3) * .09, .331, index * .71);
  });

  const clearingTrees = [[-14, 13], [-7, 8], [0, 17], [7, 10], [14, 15]] as const;
  clearingTrees.forEach(([across, depth], index) => {
    const point = terrainPoint(across, depth);
    const oak = index === 2 || index === 4;
    place(oak ? models.oak : models.spruce, point.x + ROAD_SHIFT.x, point.y + ROAD_SHIFT.y, .44 + (index % 3) * .06, oak ? 2.176 : 3.104, index * 1.17);
  });

  // A dense band along the far edge fills the top of the hero like a forest backdrop.
  for (let row = 0; row < 12; row++) {
    for (let column = 0; column < 43; column++) {
      const seed = row * 37 + column * 19;
      const across = (column - 21) * 1.82 + (row % 2) * .91 + Math.sin(seed * 2.1) * .32;
      const forestEdge = 29 + Math.sin(across * .13) * 4 + Math.sin(across * .27) * 1.25;
      const depth = forestEdge + row * 1.85 + Math.cos(seed * 1.7) * .3;
      const point = terrainPoint(across, depth);
      const oak = seed % 6 === 0;
      const model = oak ? models.oak : models.spruce;
      const scale = .48 + ((seed * 7) % 11) * .04;
      const rotation = ((seed * 137.5) % 360) * Math.PI / 180;
      place(model, point.x, point.y, scale, oak ? 2.176 : 3.104, rotation);
    }
  }
}

function addStraightRoad(parent: THREE.Object3D, source: THREE.Object3D) {
  // The Poly road runs along its local z-axis. Fit a segment to four world units
  // and repeat in both directions so each lane reaches both viewport borders.
  const bounds = new THREE.Box3().setFromObject(source);
  const center = bounds.getCenter(new THREE.Vector3());
  const size = bounds.getSize(new THREE.Vector3());
  const length = 4;
  const scale = length / size.z;
  const widthScale = 4.5 / size.x;
  for (let z = -40; z <= 40; z += length) {
    const segment = source.clone(true);
    segment.scale.set(widthScale, scale, scale);
    segment.position.set(-center.x * widthScale, -bounds.min.y * scale - .07, z - center.z * scale);
    parent.add(segment);
  }
}

function vehicleModel(parent: THREE.Object3D, source: THREE.Object3D, color: number) {
  const vehicle = new THREE.Group();
  const model = source.clone(true);
  model.scale.setScalar(.8);
  const paintedTextures = new Map<THREE.Texture, THREE.Texture>();
  model.traverse((object) => {
    if (!(object instanceof THREE.Mesh)) return;
    const recolorMaterial = (sourceMaterial: THREE.Material) => {
      const cloned = sourceMaterial.clone();
      if (object.name.startsWith("Pickup_") && cloned.name === "Atlas" && cloned instanceof THREE.MeshStandardMaterial && cloned.map) {
        let texture = paintedTextures.get(cloned.map);
        if (!texture) {
          const image = cloned.map.image;
          const canvas = document.createElement("canvas");
          canvas.width = image.width; canvas.height = image.height;
          const context = canvas.getContext("2d")!;
          context.drawImage(image, 0, 0);
          const pixels = context.getImageData(0, 0, canvas.width, canvas.height);
          // The body uses this one blue swatch in the model's texture atlas.
          // Leave the glass, lights, tires and all other atlas swatches unchanged.
          for (let i = 0; i < pixels.data.length; i += 4) {
            if (Math.abs(pixels.data[i] - 78) < 5 && Math.abs(pixels.data[i + 1] - 138) < 5 && Math.abs(pixels.data[i + 2] - 172) < 5) {
              pixels.data[i] = (color >> 16) & 255;
              pixels.data[i + 1] = (color >> 8) & 255;
              pixels.data[i + 2] = color & 255;
            }
          }
          context.putImageData(pixels, 0, 0);
          texture = new THREE.CanvasTexture(canvas);
          texture.colorSpace = cloned.map.colorSpace;
          texture.flipY = cloned.map.flipY;
          texture.wrapS = cloned.map.wrapS;
          texture.wrapT = cloned.map.wrapT;
          texture.magFilter = cloned.map.magFilter;
          texture.minFilter = cloned.map.minFilter;
          paintedTextures.set(cloned.map, texture);
        }
        cloned.map = texture;
      }
      return cloned;
    };
    object.material = Array.isArray(object.material) ? object.material.map(recolorMaterial) : recolorMaterial(object.material);
  });
  vehicle.add(model);
  parent.add(vehicle);
  vehicle.position.y = .25;
  return vehicle;
}

function normalizedAsset(source: THREE.Object3D, size: number) {
  const object = source.clone(true);
  object.updateMatrixWorld(true);
  const bounds = new THREE.Box3().setFromObject(object);
  const center = bounds.getCenter(new THREE.Vector3());
  const dimensions = bounds.getSize(new THREE.Vector3());
  const scale = size / Math.max(dimensions.x, dimensions.y, dimensions.z);
  object.scale.setScalar(scale);
  object.position.set(-center.x * scale, -center.y * scale, -center.z * scale);
  return object;
}

function boxModel(parent: THREE.Object3D, source: THREE.Object3D) {
  const parcel = new THREE.Group();
  parent.add(parcel);
  parcel.add(normalizedAsset(source, .57));
  return parcel;
}

function bombModel(parent: THREE.Object3D, source: THREE.Object3D) {
  const bomb = new THREE.Group();
  parent.add(bomb);
  bomb.add(normalizedAsset(source, .78));
  return bomb;
}

type Lane = {
  root: THREE.Group;
  truck: THREE.Group;
  ordinary: THREE.Group[];
  privateBox: THREE.Group;
  bomb: THREE.Group;
  bombSparks: THREE.Group;
  checkpoints: Checkpoint[];
  exclamation?: THREE.Group;
};
type Checkpoint = {
  z: number;
  barrier: THREE.Group;
  guard: THREE.Group;
  mixer?: THREE.AnimationMixer;
  duration: number;
  idle?: THREE.AnimationAction;
  walk?: THREE.AnimationAction;
};

// The pickup's open bed is behind the cab (negative local z after its turn).
// Truck-bound parcels follow the vehicle through the drive and return pass.
const BED_Y = .95;
function placeCargo(object: THREE.Group, truck: THREE.Group, root: THREE.Group, where: string, x: number, height: number) {
  const onTruck = where === "truck";
  const parent = onTruck ? truck : root;
  if (object.parent !== parent) parent.add(object);
  object.visible = where !== "outside" && where !== "hidden" && where !== "booth";
  if (onTruck) object.position.set(x, BED_Y + height, -.9);
  if (where === "warehouse") object.position.set(-2.8 + x, height, HOUSE_Z);
}

function buildLane(scene: THREE.Scene, position: THREE.Vector3, protectedRoute: boolean, logoUrl: string, roadTile: THREE.Object3D, sedan: THREE.Object3D, houseSource: THREE.Object3D, officerSource: THREE.Object3D, officerAnimations: THREE.AnimationClip[], boxSource: THREE.Object3D, specialBoxSource: THREE.Object3D, bombSource: THREE.Object3D, towerSource: THREE.Object3D): Lane {
  const root = new THREE.Group();
  root.position.copy(position);
  root.position.y = .5;
  // Align each local z-axis route to the camera's horizontal screen axis.
  root.rotation.y = ROAD_ANGLE;
  scene.add(root);
  addStraightRoad(root, roadTile);
  const house = houseSource.clone(true);
  house.scale.setScalar(.56);
  // Negative local x is the far side of the road from the camera.
  house.position.set(-3.81, 1.43, HOUSE_Z);
  house.rotation.y = Math.PI / 2;
  root.add(house);

  const truck = vehicleModel(root, sedan, protectedRoute ? 0x9bdc28 : 0xe85e3c);
  const ordinary = [boxModel(root, boxSource), boxModel(root, boxSource), boxModel(root, boxSource)];
  const privateBox = boxModel(root, specialBoxSource);
  privateBox.scale.setScalar(.66);
  const bomb = bombModel(root, bombSource);
  const bombSparks = new THREE.Group();
  bomb.add(bombSparks);
  const sparkColors = [0xffd04b, 0xff822e, 0xffea89, 0xffa63d];
  for (const color of sparkColors) {
    bombSparks.add(new THREE.Mesh(new THREE.OctahedronGeometry(.055), new THREE.MeshBasicMaterial({ color })));
  }
  const lane: Lane = { root, truck, ordinary, privateBox, bomb, bombSparks, checkpoints: [] };

  if (protectedRoute) {
    const texture = new THREE.TextureLoader().load(logoUrl);
    texture.colorSpace = THREE.SRGBColorSpace;
    const idle = officerAnimations.find((clip) => /idle/i.test(clip.name));
    const walk = officerAnimations.find((clip) => /walk/i.test(clip.name));
    for (const z of [INBOUND_CHECKPOINT_Z, OUTBOUND_CHECKPOINT_Z]) {
      const tower = towerSource.clone(true);
      tower.scale.setScalar(.8);
      tower.position.set(-3.96, 0, z);
      root.add(tower);
      const logoPanel = new THREE.Mesh(new THREE.BoxGeometry(.07, .72, .82), new THREE.MeshBasicMaterial({ color: 0x143f30 }));
      logoPanel.position.set(.79, 2.3, 0);
      tower.add(logoPanel);
      const logo = new THREE.Mesh(new THREE.PlaneGeometry(.55, .55), new THREE.MeshBasicMaterial({ map: texture, transparent: true, side: THREE.DoubleSide }));
      logo.position.set(.831, 2.3, 0);
      logo.rotation.y = Math.PI / 2;
      tower.add(logo);

      const guard = cloneSkinnedModel(officerSource) as THREE.Group;
      guard.position.set(-3.15, 0, z + 1.05);
      guard.scale.setScalar(.7);
      guard.rotation.y = Math.PI;
      root.add(guard);
      const mixer = idle || walk ? new THREE.AnimationMixer(guard) : undefined;
      const idleAction = idle ? mixer!.clipAction(idle) : undefined;
      const walkAction = walk ? mixer!.clipAction(walk) : undefined;
      idleAction?.play();
      walkAction?.play();
      walkAction?.setEffectiveWeight(0);

      const barrier = new THREE.Group();
      barrier.position.set(-1.7, .09, z);
      root.add(barrier);
      cuboid(root, .24, 1.17, .24, materials.stripe, -1.7, .5, z);
      const arm = cuboid(barrier, 3.25, .15, .15, materials.stripe, 1.62, .98, 0);
      for (let i = 0; i < 7; i++) cuboid(arm, .19, .16, .16, materials.danger, -1.42 + i * .48, 0, 0);
      lane.checkpoints.push({ z, barrier, guard, mixer, duration: Math.max(idle?.duration ?? 0, walk?.duration ?? 0, 3), idle: idleAction, walk: walkAction });
    }
  } else {
    const exclamation = new THREE.Group();
    root.add(exclamation); exclamation.position.set(-1.68, .6, -7);
    cuboid(exclamation, .22, .97, .16, materials.danger, 0, .66, 0);
    cuboid(exclamation, .25, .22, .18, materials.danger, 0, -.04, 0);
    lane.exclamation = exclamation;
  }
  return lane;
}

export async function createHeroScene(canvas: HTMLCanvasElement, logoUrl: string, roadUrl: string, carUrl: string, houseUrl: string, officerUrl: string, boxUrl: string, specialBoxUrl: string, bombUrl: string, towerUrl: string, sceneryUrls: { rock: string; spruce: string; oak: string }) {
  const renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true, powerPreference: "low-power" });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  const scene = new THREE.Scene();
  scene.add(new THREE.HemisphereLight(0xeaffdd, 0x27452f, .45));
  const sunlight = new THREE.DirectionalLight(0xffefd6, 3.2);
  sunlight.position.set(-12, 7, 10); scene.add(sunlight);
  const loader = new GLTFLoader();
  const draco = new DRACOLoader();
  draco.setDecoderPath(`${import.meta.env.BASE_URL}draco/`);
  loader.setDRACOLoader(draco);
  const [road, sedan, house, officer, box, specialBox, bomb, tower, rock, spruce, oak] = await Promise.all([loader.loadAsync(roadUrl), loader.loadAsync(carUrl), loader.loadAsync(houseUrl), loader.loadAsync(officerUrl), loader.loadAsync(boxUrl), loader.loadAsync(specialBoxUrl), loader.loadAsync(bombUrl), loader.loadAsync(towerUrl), loader.loadAsync(sceneryUrls.rock), loader.loadAsync(sceneryUrls.spruce), loader.loadAsync(sceneryUrls.oak)]);
  draco.dispose();
  addFacetedGround(scene);
  addScenery(scene, { rock: rock.scene, spruce: spruce.scene, oak: oak.scene });
  const unsafe = buildLane(scene, new THREE.Vector3(ROAD_CENTERS[0][0], 0, ROAD_CENTERS[0][1]), false, logoUrl, road.scene, sedan.scene, house.scene, officer.scene, officer.animations, box.scene, specialBox.scene, bomb.scene, tower.scene);
  const safe = buildLane(scene, new THREE.Vector3(ROAD_CENTERS[1][0], 0, ROAD_CENTERS[1][1]), true, logoUrl, road.scene, sedan.scene, house.scene, officer.scene, officer.animations, box.scene, specialBox.scene, bomb.scene, tower.scene);
  const camera = new THREE.OrthographicCamera(-15, 15, 7, -7, .1, 100);
  camera.position.set(16, 23, 27);
  camera.lookAt(0, .3, 0);
  let width = 0; let height = 0;

  function getViewportExit(lane: Lane) {
    camera.updateMatrixWorld();
    const screenX = (z: number) => lane.root.localToWorld(new THREE.Vector3(0, 1.2, z)).project(camera).x;
    const edge = (direction: number) => {
      let low = 0, high = 64;
      for (let i = 0; i < 20; i++) {
        const mid = (low + high) / 2;
        if (direction * screenX(direction * mid) < -1.1) high = mid;
        else low = mid;
      }
      return high;
    };
    return Math.max(edge(-1), edge(1));
  }

  function pose(lane: Lane, elapsed: number, protectedRoute: boolean, exitZ: number) {
    const state = routeAt(elapsed, protectedRoute, exitZ);
    lane.truck.visible = state.visible;
    lane.truck.position.z = state.z;
    lane.truck.rotation.y = Math.PI;
    lane.ordinary.forEach((parcel, i) => {
      parcel.visible = !state.inbound && state.visible && i < state.ordinary;
      if (parcel.parent !== lane.truck) lane.truck.add(parcel);
      parcel.scale.setScalar(.66);
      parcel.position.set((i - 1) * .43, BED_Y + .2, -1.3);
    });
    placeCargo(lane.privateBox, lane.truck, lane.root, state.privateBox, .28, .2);
    if (state.privateBox === "truck" && !state.inbound && elapsed % 20 < 2.1) {
      const progress = Math.max(0, Math.min(1, (elapsed % 20 - 1.2) / .9));
      lane.root.add(lane.privateBox);
      lane.privateBox.position.set(
        THREE.MathUtils.lerp(-2.52, -.28, progress),
        THREE.MathUtils.lerp(.2, lane.truck.position.y + BED_Y + .2, progress) + Math.sin(progress * Math.PI) * .7,
        THREE.MathUtils.lerp(HOUSE_Z, state.z + .9, progress),
      );
    }
    placeCargo(lane.bomb, lane.truck, lane.root, state.bomb, -.2, .39);
    // The skull is on the bomb's +z face; keep it facing the viewer even
    // while the bomb is parented to the truck rotated by half a turn.
    lane.bomb.rotation.y = state.bomb === "truck" ? -Math.PI / 2 : Math.PI / 2;
    lane.bombSparks.visible = lane.bomb.visible;
    lane.bombSparks.children.forEach((spark, i) => {
      const phase = elapsed * 13 + i * 2.3;
      spark.position.set(Math.sin(phase) * .12, .43 + i * .047 + Math.cos(phase * 1.3) * .045, Math.cos(phase) * .1);
      spark.scale.setScalar(.75 + .25 * Math.sin(phase * 1.7));
    });
    for (const checkpoint of lane.checkpoints) {
      const active = state.inbound === (checkpoint.z > 0);
      const progress = active ? state.guardReach : 0;
      const fromX = -3.15;
      const toX = -2.75;
      checkpoint.guard.position.x = THREE.MathUtils.lerp(fromX, toX, progress);
      checkpoint.guard.position.z = THREE.MathUtils.lerp(checkpoint.z + 1.05, checkpoint.z + .45, progress);
      if (progress >= 1) {
        checkpoint.guard.rotation.y = Math.atan2(-checkpoint.guard.position.x, lane.truck.position.z - checkpoint.guard.position.z);
      } else if (active && state.guardWalking) {
        checkpoint.guard.rotation.y = Math.atan2(toX - fromX, -.6);
      } else {
        checkpoint.guard.rotation.y = Math.PI;
      }
      checkpoint.guard.rotation.z = 0;
      checkpoint.idle?.setEffectiveWeight(active && state.guardWalking ? 0 : 1);
      checkpoint.walk?.setEffectiveWeight(active && state.guardWalking ? 1 : 0);
      checkpoint.mixer?.setTime(elapsed % checkpoint.duration);
      const time = ((elapsed % 20) + 20) % 20;
      const smooth = (value: number) => {
        const t = THREE.MathUtils.clamp(value, 0, 1);
        return t * t * (3 - 2 * t);
      };
      let opening = 0;
      if (checkpoint.z < 0) {
        if (time >= 6.1 && time < 6.9) opening = smooth((time - 6.1) / .8);
        else if (time >= 6.9 && time < 9.4) opening = 1;
        else if (time >= 9.4 && time < 10.2) opening = 1 - smooth((time - 9.4) / .8);
      } else {
        if (time >= 16 && time < 16.8) opening = smooth((time - 16) / .8);
        else if (time >= 16.8 && time < 18.7) opening = 1;
        else if (time >= 18.7 && time < 19.5) opening = 1 - smooth((time - 18.7) / .8);
      }
      checkpoint.barrier.rotation.z = opening * 1.25;
    }
    // The seized item leaves the bed, reaches the far-side guard's hands,
    // stays there briefly, and only then disappears into the checkpoint.
    const moveToGuard = (object: THREE.Group, height: number, checkpoint: Checkpoint, start: number, arrival: number, x: number) => {
      const progress = THREE.MathUtils.smoothstep(elapsed % 20, start, arrival);
      object.position.set(
        THREE.MathUtils.lerp(x, checkpoint.guard.position.x + .45, progress),
        THREE.MathUtils.lerp(lane.truck.position.y + BED_Y + height, .85, progress) + Math.sin(progress * Math.PI) * .25,
        THREE.MathUtils.lerp(lane.truck.position.z + .9, checkpoint.guard.position.z + .55, progress),
      );
    };
    if (state.privateBox === "handoff") moveToGuard(lane.privateBox, .2, lane.checkpoints[1], 4.25, 5.25, -.28);
    if (state.bomb === "handoff") moveToGuard(lane.bomb, .39, lane.checkpoints[0], 14, 15.1, .2);
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
        const span = 19.5;
        camera.left = -span / 2; camera.right = span / 2;
        camera.top = span / (2 * aspect); camera.bottom = -camera.top;
        camera.position.set(lane.root.position.x + 10, 18, 16.875);
        camera.lookAt(lane.root.position.x, .1, 0);
        lane.root.rotation.y = ROAD_ANGLE;
        camera.updateProjectionMatrix();
        pose(lane, elapsed, lane === safe, getViewportExit(lane));
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
      unsafe.root.rotation.y = safe.root.rotation.y = ROAD_ANGLE;
      camera.updateProjectionMatrix();
      pose(unsafe, elapsed, false, getViewportExit(unsafe));
      pose(safe, elapsed, true, getViewportExit(safe));
      renderer.render(scene, camera);
    }
  }

  function dispose() {
    renderer.setAnimationLoop(null);
    const geometries = new Set<THREE.BufferGeometry>();
    const usedMaterials = new Set<THREE.Material>();
    const usedTextures = new Set<THREE.Texture>();
    scene.traverse((object) => {
      if (object instanceof THREE.Mesh) {
        geometries.add(object.geometry);
        (Array.isArray(object.material) ? object.material : [object.material]).forEach((mat) => usedMaterials.add(mat));
      }
    });
    geometries.forEach((geometry) => geometry.dispose());
    usedMaterials.forEach((mat) => {
      for (const value of Object.values(mat)) if (value instanceof THREE.Texture) usedTextures.add(value);
      mat.dispose();
    });
    usedTextures.forEach((texture) => texture.dispose());
    renderer.dispose();
  }
  return { render, dispose };
}
