// CivQuest Blueprint Studio - Vanilla WebGL 3D First-Person Walkthrough Mode
// Zero external dependencies. WASD movement, mouse-look with Pointer Lock, ESC to exit.
// Includes column-major matrix math, proper WebGL perspective and view matrices,
// and a robust 2D canvas software 3D perspective projection fallback.

(function () {
  const BLOCK_COLORS = {
    "minecraft:oak_planks": [0.78, 0.54, 0.30],
    "minecraft:spruce_planks": [0.47, 0.33, 0.23],
    "minecraft:birch_planks": [0.84, 0.77, 0.60],
    "minecraft:jungle_planks": [0.67, 0.47, 0.34],
    "minecraft:acacia_planks": [0.66, 0.35, 0.20],
    "minecraft:dark_oak_planks": [0.26, 0.17, 0.08],
    "minecraft:mangrove_planks": [0.46, 0.21, 0.19],
    "minecraft:cherry_planks": [0.89, 0.65, 0.66],
    "minecraft:bamboo_planks": [0.76, 0.67, 0.30],
    "minecraft:oak_log": [0.43, 0.31, 0.20],
    "minecraft:spruce_log": [0.24, 0.16, 0.09],
    "minecraft:birch_log": [0.87, 0.85, 0.81],
    "minecraft:dark_oak_log": [0.21, 0.14, 0.07],
    "minecraft:stone": [0.50, 0.50, 0.50],
    "minecraft:cobblestone": [0.42, 0.42, 0.42],
    "minecraft:stone_bricks": [0.47, 0.47, 0.47],
    "minecraft:deepslate": [0.23, 0.23, 0.27],
    "minecraft:deepslate_bricks": [0.16, 0.16, 0.20],
    "minecraft:cobbled_deepslate": [0.20, 0.20, 0.24],
    "minecraft:glass": [0.65, 0.85, 0.95],
    "minecraft:glass_pane": [0.65, 0.85, 0.95],
    "minecraft:oak_stairs": [0.78, 0.54, 0.30],
    "minecraft:spruce_stairs": [0.47, 0.33, 0.23],
    "minecraft:stone_brick_stairs": [0.47, 0.47, 0.47],
    "minecraft:torch": [0.95, 0.61, 0.07],
    "minecraft:lantern": [0.95, 0.77, 0.06],
    "minecraft:barrel": [0.53, 0.34, 0.17],
    "minecraft:chest": [0.62, 0.41, 0.16],
    "minecraft:crafting_table": [0.64, 0.45, 0.25],
    "minecraft:furnace": [0.33, 0.33, 0.33],
    "minecraft:red_bed": [0.75, 0.22, 0.17],
    "minecraft:oak_door": [0.55, 0.39, 0.22],
    "minecraft:spruce_door": [0.45, 0.30, 0.18],
    "minecraft:ladder": [0.70, 0.54, 0.31]
  };

  function resolveBlockRGB(blockState) {
    if (!blockState) return [0.5, 0.5, 0.5];
    let cleanId = blockState.split("[")[0].trim().toLowerCase();
    if (cleanId.startsWith("#")) cleanId = cleanId.substring(1);
    if (BLOCK_COLORS[cleanId]) return BLOCK_COLORS[cleanId];

    // Taxonomy tag heuristics
    if (cleanId.includes("plank")) return [0.78, 0.54, 0.30];
    if (cleanId.includes("log") || cleanId.includes("wood")) return [0.43, 0.31, 0.20];
    if (cleanId.includes("stair")) return [0.75, 0.52, 0.28];
    if (cleanId.includes("slab")) return [0.75, 0.52, 0.28];
    if (cleanId.includes("door") && !cleanId.includes("trapdoor")) {
      if (blockState.includes("half=upper")) return [0.65, 0.48, 0.32];
      return [0.55, 0.39, 0.22];
    }
    if (cleanId.includes("trapdoor") || cleanId.includes("fence")) return [0.55, 0.39, 0.22];
    if (cleanId.includes("cobble")) return [0.42, 0.42, 0.42];
    if (cleanId.includes("stone_brick")) return [0.47, 0.47, 0.47];
    if (cleanId.includes("stone")) return [0.50, 0.50, 0.50];
    if (cleanId.includes("deepslate")) return [0.23, 0.23, 0.27];
    if (cleanId.includes("glass")) return [0.65, 0.85, 0.95];
    if (cleanId.includes("torch") || cleanId.includes("light")) return [0.95, 0.61, 0.07];
    if (cleanId.includes("bed")) {
      if (blockState.includes("part=head")) return [0.82, 0.24, 0.18];
      return [0.75, 0.22, 0.17];
    }
    if (cleanId.includes("chest") || cleanId.includes("barrel")) return [0.58, 0.38, 0.17];

    // Deterministic hash color fallback
    let hash = 0;
    for (let i = 0; i < cleanId.length; i++) {
      hash = cleanId.charCodeAt(i) + ((hash << 5) - hash);
    }
    const h = (Math.abs(hash) % 360) / 360;
    return hslToRgb(h, 0.45, 0.45);
  }

  function hslToRgb(h, s, l) {
    let r, g, b;
    if (s === 0) {
      r = g = b = l;
    } else {
      const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
      const p = 2 * l - q;
      r = hueToRgb(p, q, h + 1 / 3);
      g = hueToRgb(p, q, h);
      b = hueToRgb(p, q, h - 1 / 3);
    }
    return [r, g, b];
  }

  function hueToRgb(p, q, t) {
    if (t < 0) t += 1;
    if (t > 1) t -= 1;
    if (t < 1 / 6) return p + (q - p) * 6 * t;
    if (t < 1 / 2) return q;
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6;
    return p;
  }

  // Column-Major 4x4 Matrix Utilities for WebGL
  function mat4Perspective(fovRad, aspect, near, far) {
    const f = 1.0 / Math.tan(fovRad / 2.0);
    const nf = 1.0 / (near - far);
    return new Float32Array([
      f / aspect, 0, 0, 0,
      0, f, 0, 0,
      0, 0, (far + near) * nf, -1,
      0, 0, (2.0 * far * near) * nf, 0
    ]);
  }

  function mat4Multiply(a, b) {
    const out = new Float32Array(16);
    for (let c = 0; c < 4; c++) {
      for (let r = 0; r < 4; r++) {
        out[c * 4 + r] =
          a[0 * 4 + r] * b[c * 4 + 0] +
          a[1 * 4 + r] * b[c * 4 + 1] +
          a[2 * 4 + r] * b[c * 4 + 2] +
          a[3 * 4 + r] * b[c * 4 + 3];
      }
    }
    return out;
  }

  function mat4LookAt(eye, target, up) {
    let z0 = eye[0] - target[0];
    let z1 = eye[1] - target[1];
    let z2 = eye[2] - target[2];
    let len = Math.hypot(z0, z1, z2);
    if (len === 0) {
      z2 = 1;
    } else {
      z0 /= len;
      z1 /= len;
      z2 /= len;
    }

    // x = cross(up, z)
    let x0 = up[1] * z2 - up[2] * z1;
    let x1 = up[2] * z0 - up[0] * z2;
    let x2 = up[0] * z1 - up[1] * z0;
    len = Math.hypot(x0, x1, x2);
    if (len === 0) {
      x0 = 1; x1 = 0; x2 = 0;
    } else {
      x0 /= len;
      x1 /= len;
      x2 /= len;
    }

    // y = cross(z, x)
    const y0 = z1 * x2 - z2 * x1;
    const y1 = z2 * x0 - z0 * x2;
    const y2 = z0 * x1 - z1 * x0;

    return new Float32Array([
      x0, y0, z0, 0,
      x1, y1, z1, 0,
      x2, y2, z2, 0,
      -(x0 * eye[0] + x1 * eye[1] + x2 * eye[2]),
      -(y0 * eye[0] + y1 * eye[1] + y2 * eye[2]),
      -(z0 * eye[0] + z1 * eye[1] + z2 * eye[2]),
      1
    ]);
  }

  class WalkthroughRenderer {
    constructor(canvasId, hudId, onExitCallback) {
      this.canvas = document.getElementById(canvasId);
      this.hud = document.getElementById(hudId);
      this.onExitCallback = onExitCallback || (() => {});
      this.blueprint = null;
      this.isActive = false;

      // Camera State
      this.pos = { x: 0, y: 1.62, z: 0 };
      this.yaw = 0;   // Radians: 0 = looking towards -Z (North)
      this.pitch = 0; // Radians: positive looks up, negative looks down
      this.keys = {};
      this.lastFrameTime = performance.now();
      this.animFrameId = null;

      // WebGL State
      this.gl = null;
      this.program = null;
      this.vbo = null;
      this.vertexCount = 0;
      this.uMatrixLoc = null;
      this.use2DFallback = false;

      // Raw geometry cache for 2D software fallback
      this.softwareFaces = [];

      if (this.canvas) {
        this.initWebGL();
        this.initEventListeners();
      }
    }

    initWebGL() {
      try {
        const gl = this.canvas.getContext("webgl", { antialias: true, depth: true }) ||
                   this.canvas.getContext("experimental-webgl");
        if (!gl) {
          console.warn("[Walkthrough] WebGL context unavailable. Activating 2D software renderer fallback.");
          this.use2DFallback = true;
          return;
        }
        this.gl = gl;

        // Shaders
        const vsSource = `
          attribute vec3 aPosition;
          attribute vec3 aColor;
          uniform mat4 uMatrix;
          varying vec3 vColor;
          void main() {
            gl_Position = uMatrix * vec4(aPosition, 1.0);
            vColor = aColor;
          }
        `;

        const fsSource = `
          precision mediump float;
          varying vec3 vColor;
          void main() {
            gl_FragColor = vec4(vColor, 1.0);
          }
        `;

        const vs = this.compileShader(gl.VERTEX_SHADER, vsSource);
        const fs = this.compileShader(gl.FRAGMENT_SHADER, fsSource);
        if (!vs || !fs) {
          throw new Error("Shader compilation failed");
        }

        const prog = gl.createProgram();
        gl.attachShader(prog, vs);
        gl.attachShader(prog, fs);
        gl.linkProgram(prog);

        if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
          console.error("[Walkthrough] Shader link error:", gl.getProgramInfoLog(prog));
          this.gl = null;
          this.use2DFallback = true;
          return;
        }

        this.program = prog;
        this.uMatrixLoc = gl.getUniformLocation(prog, "uMatrix");
        this.vbo = gl.createBuffer();
        this.use2DFallback = false;
      } catch (err) {
        console.warn("[Walkthrough] Error initializing WebGL:", err, ". Using 2D software fallback.");
        this.gl = null;
        this.use2DFallback = true;
      }
    }

    compileShader(type, source) {
      if (!this.gl) return null;
      const gl = this.gl;
      const s = gl.createShader(type);
      gl.shaderSource(s, source);
      gl.compileShader(s);
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
        console.error("[Walkthrough] Shader compilation error:", gl.getShaderInfoLog(s));
        gl.deleteShader(s);
        return null;
      }
      return s;
    }

    initEventListeners() {
      // Key tracking
      window.addEventListener("keydown", (e) => {
        if (!this.isActive) return;
        if (e.code === "Escape") {
          this.exit();
          return;
        }
        this.keys[e.code] = true;
      });

      window.addEventListener("keyup", (e) => {
        if (!this.isActive) return;
        this.keys[e.code] = false;
      });

      // Mouse Look via Pointer Lock API
      document.addEventListener("mousemove", (e) => {
        if (!this.isActive || document.pointerLockElement !== this.canvas) return;
        const sensitivity = 0.0022;
        this.yaw -= e.movementX * sensitivity;
        this.pitch -= e.movementY * sensitivity;

        // Clamp pitch to [-85 deg, +85 deg]
        const maxPitch = (85 * Math.PI) / 180;
        this.pitch = Math.max(-maxPitch, Math.min(maxPitch, this.pitch));
      });

      const onLockChange = () => {
        if (this.isActive && document.pointerLockElement !== this.canvas) {
          this.exit();
        }
      };
      document.addEventListener("pointerlockchange", onLockChange);
      document.addEventListener("mozpointerlockchange", onLockChange);

      this.canvas.addEventListener("click", () => {
        if (this.isActive && document.pointerLockElement !== this.canvas) {
          this.requestPointerLock();
        }
      });
    }

    requestPointerLock() {
      if (!this.canvas) return;
      const req = this.canvas.requestPointerLock || this.canvas.mozRequestPointerLock;
      if (req) {
        req.call(this.canvas);
      }
    }

    exitPointerLock() {
      if (document.exitPointerLock) {
        document.exitPointerLock();
      } else if (document.mozExitPointerLock) {
        document.mozExitPointerLock();
      }
    }

    setBlueprint(bp) {
      this.blueprint = bp;
      this.rebuildMesh();
    }

    rebuildMesh() {
      if (!this.blueprint) return;
      const dims = this.blueprint.dimensions || { width: 7, height: 4, length: 5, min_y: 0 };
      const minY = dims.min_y !== undefined ? dims.min_y : 0;
      const palette = this.blueprint.palette || {};

      // 1. Build a 3D block lookup for neighbor face culling
      const blockMap = new Map();
      const voxels = [];

      if (this.blueprint.layers && this.blueprint.layers.length > 0) {
        for (const layer of this.blueprint.layers) {
          const y = layer.y;
          if (!layer.matrix) continue;
          for (let z = 0; z < layer.matrix.length; z++) {
            const row = layer.matrix[z];
            for (let x = 0; x < row.length; x++) {
              const palIdx = row[x];
              if (palIdx > 0 && palette[palIdx]) {
                const state = palette[palIdx];
                const key = `${x},${y},${z}`;
                blockMap.set(key, state);
                voxels.push({ x, y, z, state });
              }
            }
          }
        }
      } else if (this.blueprint.blocks && this.blueprint.blocks.length > 0) {
        for (const blk of this.blueprint.blocks) {
          const key = `${blk.x},${blk.y},${blk.z}`;
          blockMap.set(key, blk.state);
          voxels.push({ x: blk.x, y: blk.y, z: blk.z, state: blk.state });
        }
      }

      const hasBlock = (x, y, z) => {
        const state = blockMap.get(`${x},${y},${z}`);
        if (!state) return false;
        if (state.includes("glass") || state.includes("door") || state.includes("torch")) return false;
        return true;
      };

      // 2. Generate Triangles for all exposed block faces
      const verts = []; // x, y, z, r, g, b
      const rawFaces = [];

      const addFace = (p1, p2, p3, p4, r, g, b, shade) => {
        const sr = r * shade;
        const sg = g * shade;
        const sb = b * shade;
        verts.push(
          p1[0], p1[1], p1[2], sr, sg, sb,
          p2[0], p2[1], p2[2], sr, sg, sb,
          p3[0], p3[1], p3[2], sr, sg, sb,
          p1[0], p1[1], p1[2], sr, sg, sb,
          p3[0], p3[1], p3[2], sr, sg, sb,
          p4[0], p4[1], p4[2], sr, sg, sb
        );
        rawFaces.push({
          pts: [p1, p2, p3, p4],
          color: [sr, sg, sb],
          cx: (p1[0] + p2[0] + p3[0] + p4[0]) / 4,
          cy: (p1[1] + p2[1] + p3[1] + p4[1]) / 4,
          cz: (p1[2] + p2[2] + p3[2] + p4[2]) / 4
        });
      };

      for (const v of voxels) {
        const x = v.x;
        const y = v.y;
        const z = v.z;
        const [r, g, b] = resolveBlockRGB(v.state);

        // Top Face (+Y)
        if (!hasBlock(x, y + 1, z)) {
          let topR = r, topG = g, topB = b;
          if (v.state && v.state.includes("bed") && (v.state.includes("part=head") || v.state.includes("head"))) {
            topR = 0.94; topG = 0.94; topB = 0.96; // Pillow shading on bed head
          }
          addFace(
            [x, y + 1, z], [x, y + 1, z + 1], [x + 1, y + 1, z + 1], [x + 1, y + 1, z],
            topR, topG, topB, 1.0
          );
        }

        // Bottom Face (-Y)
        if (!hasBlock(x, y - 1, z)) {
          addFace(
            [x, y, z], [x + 1, y, z], [x + 1, y, z + 1], [x, y, z + 1],
            r, g, b, 0.45
          );
        }

        // North Face (-Z)
        if (!hasBlock(x, y, z - 1)) {
          addFace(
            [x, y, z], [x, y + 1, z], [x + 1, y + 1, z], [x + 1, y, z],
            r, g, b, 0.75
          );
        }

        // South Face (+Z)
        if (!hasBlock(x, y, z + 1)) {
          addFace(
            [x, y, z + 1], [x + 1, y, z + 1], [x + 1, y + 1, z + 1], [x, y + 1, z + 1],
            r, g, b, 0.85
          );
        }

        // West Face (-X)
        if (!hasBlock(x - 1, y, z)) {
          addFace(
            [x, y, z], [x, y, z + 1], [x, y + 1, z + 1], [x, y + 1, z],
            r, g, b, 0.65
          );
        }

        // East Face (+X)
        if (!hasBlock(x + 1, y, z)) {
          addFace(
            [x + 1, y, z], [x + 1, y + 1, z], [x + 1, y + 1, z + 1], [x + 1, y, z + 1],
            r, g, b, 0.70
          );
        }
      }

      // 3. Ground plane grid around building
      const groundMinX = -12;
      const groundMaxX = dims.width + 12;
      const groundMinZ = -12;
      const groundMaxZ = dims.length + 12;
      const groundY = minY;

      for (let gx = groundMinX; gx < groundMaxX; gx += 2) {
        for (let gz = groundMinZ; gz < groundMaxZ; gz += 2) {
          const isCheck = (Math.abs(Math.floor(gx / 2) + Math.floor(gz / 2))) % 2 === 0;
          const [gr, gg, gb] = isCheck ? [0.12, 0.16, 0.22] : [0.15, 0.20, 0.28];
          addFace(
            [gx, groundY, gz],
            [gx, groundY, gz + 2],
            [gx + 2, groundY, gz + 2],
            [gx + 2, groundY, gz],
            gr, gg, gb, 0.9
          );
        }
      }

      this.softwareFaces = rawFaces;

      if (this.gl && this.vbo) {
        const gl = this.gl;
        gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(verts), gl.STATIC_DRAW);
        this.vertexCount = verts.length / 6;
      }
    }

    enter() {
      this.isActive = true;
      this.keys = {};

      // Match canvas dimensions to isometric canvas viewport
      const isoCanvas = document.getElementById("isometric-canvas");
      if (isoCanvas) {
        this.canvas.width = isoCanvas.width;
        this.canvas.height = isoCanvas.height;
      }

      const dims = this.blueprint && this.blueprint.dimensions
        ? this.blueprint.dimensions
        : { width: 7, height: 4, length: 5, min_y: 0 };
      const minY = dims.min_y !== undefined ? dims.min_y : 0;

      // Position just outside front door, facing North (-Z) at eye height Y = minY + 1.62
      this.pos.x = dims.width / 2;
      this.pos.y = minY + 1.62;
      this.pos.z = dims.length + 2.0;
      this.yaw = 0;   // Facing North into the doorway
      this.pitch = 0; // Level horizon

      // Show 3D canvas & HUD
      this.canvas.classList.remove("hidden");
      if (this.hud) this.hud.classList.remove("hidden");

      // Hide 2.5D canvas
      if (isoCanvas) isoCanvas.classList.add("hidden");

      // Request Pointer Lock
      this.requestPointerLock();

      // Ensure mesh is up to date
      this.rebuildMesh();

      // Start render loop
      this.lastFrameTime = performance.now();
      const loop = (now) => {
        if (!this.isActive) return;
        this.update(now);
        this.render();
        this.animFrameId = requestAnimationFrame(loop);
      };
      this.animFrameId = requestAnimationFrame(loop);
    }

    exit() {
      if (!this.isActive) return;
      this.isActive = false;

      if (this.animFrameId) {
        cancelAnimationFrame(this.animFrameId);
        this.animFrameId = null;
      }

      this.exitPointerLock();

      // Hide 3D canvas & HUD
      this.canvas.classList.add("hidden");
      if (this.hud) this.hud.classList.add("hidden");

      // Restore 2.5D isometric canvas
      const isoCanvas = document.getElementById("isometric-canvas");
      if (isoCanvas) isoCanvas.classList.remove("hidden");

      // Notify callback to sync toolbar buttons
      this.onExitCallback();
    }

    update(now) {
      const dt = Math.min((now - this.lastFrameTime) / 1000, 0.1);
      this.lastFrameTime = now;

      // Movement vector on X/Z horizontal plane based on Yaw
      const moveFwdX = Math.sin(this.yaw);
      const moveFwdZ = -Math.cos(this.yaw);
      const moveRightX = Math.cos(this.yaw);
      const moveRightZ = Math.sin(this.yaw);

      let mx = 0;
      let mz = 0;
      let my = 0;

      // WASD / Arrow Keys
      if (this.keys["KeyW"] || this.keys["ArrowUp"]) {
        mx += moveFwdX;
        mz += moveFwdZ;
      }
      if (this.keys["KeyS"] || this.keys["ArrowDown"]) {
        mx -= moveFwdX;
        mz -= moveFwdZ;
      }
      if (this.keys["KeyA"] || this.keys["ArrowLeft"]) {
        mx -= moveRightX;
        mz -= moveRightZ;
      }
      if (this.keys["KeyD"] || this.keys["ArrowRight"]) {
        mx += moveRightX;
        mz += moveRightZ;
      }

      // Fly Up / Down
      if (this.keys["Space"]) {
        my += 1.0;
      }
      if (this.keys["ShiftLeft"] || this.keys["ShiftRight"]) {
        my -= 1.0;
      }

      const hLen = Math.hypot(mx, mz);
      if (hLen > 0) {
        mx /= hLen;
        mz /= hLen;
      }

      const speed = 5.2; // blocks per second
      this.pos.x += mx * speed * dt;
      this.pos.z += mz * speed * dt;
      this.pos.y += my * speed * dt;
    }

    render() {
      if (this.gl && !this.use2DFallback && this.program && this.vertexCount > 0) {
        this.renderWebGL();
      } else {
        this.render2DFallback();
      }
    }

    renderWebGL() {
      const gl = this.gl;
      gl.viewport(0, 0, this.canvas.width, this.canvas.height);

      // Skybox background color (dark blueprint slate)
      gl.clearColor(0.06, 0.08, 0.12, 1.0);
      gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);

      gl.enable(gl.DEPTH_TEST);
      gl.depthFunc(gl.LEQUAL);

      // Proper WebGL column-major Perspective Projection (FOV 75°, near 0.1, far 200.0)
      const aspect = this.canvas.width / this.canvas.height;
      const fovRad = (75 * Math.PI) / 180.0;
      const proj = mat4Perspective(fovRad, aspect, 0.1, 200.0);

      // View Matrix Construction
      const cosP = Math.cos(this.pitch);
      const sinP = Math.sin(this.pitch);
      const cosY = Math.cos(this.yaw);
      const sinY = Math.sin(this.yaw);

      // Forward gaze vector: yaw=0 points towards North (-Z)
      const fwd = [sinY * cosP, sinP, -cosY * cosP];
      const eye = [this.pos.x, this.pos.y, this.pos.z];
      const target = [eye[0] + fwd[0], eye[1] + fwd[1], eye[2] + fwd[2]];
      const up = [0, 1, 0];

      const view = mat4LookAt(eye, target, up);
      const mvp = mat4Multiply(proj, view);

      gl.useProgram(this.program);
      gl.uniformMatrix4fv(this.uMatrixLoc, false, mvp);

      gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo);

      const stride = 6 * Float32Array.BYTES_PER_ELEMENT;
      const aPosLoc = gl.getAttribLocation(this.program, "aPosition");
      const aColLoc = gl.getAttribLocation(this.program, "aColor");

      gl.enableVertexAttribArray(aPosLoc);
      gl.vertexAttribPointer(aPosLoc, 3, gl.FLOAT, false, stride, 0);

      gl.enableVertexAttribArray(aColLoc);
      gl.vertexAttribPointer(aColLoc, 3, gl.FLOAT, false, stride, 3 * Float32Array.BYTES_PER_ELEMENT);

      gl.drawArrays(gl.TRIANGLES, 0, this.vertexCount);
    }

    // 2D Software 3D Perspective Projection Fallback
    // Guarantees walkthrough NEVER fails or displays a blank screen
    render2DFallback() {
      const ctx = this.canvas.getContext("2d");
      if (!ctx) return;

      const w = this.canvas.width;
      const h = this.canvas.height;

      // Dark sky background
      ctx.fillStyle = "#0f141f";
      ctx.fillRect(0, 0, w, h);

      // Horizon split for ground visual cue
      const pitchRatio = this.pitch / ((85 * Math.PI) / 180);
      const horizonY = (h / 2) + pitchRatio * (h / 2);
      ctx.fillStyle = "#161b26";
      ctx.fillRect(0, Math.max(0, horizonY), w, Math.max(0, h - horizonY));

      const cosP = Math.cos(this.pitch);
      const sinP = Math.sin(this.pitch);
      const cosY = Math.cos(this.yaw);
      const sinY = Math.sin(this.yaw);

      // Camera vectors
      const fwd = [sinY * cosP, sinP, -cosY * cosP];
      const right = [cosY, 0, sinY];
      // up = cross(fwd, right)
      const up = [
        fwd[1] * right[2] - fwd[2] * right[1],
        fwd[2] * right[0] - fwd[0] * right[2],
        fwd[0] * right[1] - fwd[1] * right[0]
      ];

      const fovRad = (75 * Math.PI) / 180.0;
      const fPixel = (h / 2) / Math.tan(fovRad / 2);
      const cx = w / 2;
      const cy = h / 2;

      // Transform, sort, and draw faces
      const sortedFaces = [];
      const eye = this.pos;

      for (const f of this.softwareFaces) {
        // Center distance along fwd
        const dx = f.cx - eye.x;
        const dy = f.cy - eye.y;
        const dz = f.cz - eye.z;
        const depth = dx * fwd[0] + dy * fwd[1] + dz * fwd[2];
        if (depth <= 0.15) continue; // Behind or too close

        sortedFaces.push({ face: f, depth: depth });
      }

      // Painter's algorithm: draw furthest to closest
      sortedFaces.sort((a, b) => b.depth - a.depth);

      for (const item of sortedFaces) {
        const f = item.face;
        const pts = [];
        let allValid = true;

        for (const p of f.pts) {
          const vx = p[0] - eye.x;
          const vy = p[1] - eye.y;
          const vz = p[2] - eye.z;

          const zCam = vx * fwd[0] + vy * fwd[1] + vz * fwd[2];
          if (zCam <= 0.05) {
            allValid = false;
            break;
          }

          const xCam = vx * right[0] + vy * right[1] + vz * right[2];
          const yCam = vx * up[0] + vy * up[1] + vz * up[2];

          const sx = cx + (xCam / zCam) * fPixel;
          const sy = cy - (yCam / zCam) * fPixel;
          pts.push([sx, sy]);
        }

        if (!allValid || pts.length < 3) continue;

        ctx.beginPath();
        ctx.moveTo(pts[0][0], pts[0][1]);
        for (let i = 1; i < pts.length; i++) {
          ctx.lineTo(pts[i][0], pts[i][1]);
        }
        ctx.closePath();

        const [r, g, b] = f.color;
        ctx.fillStyle = `rgb(${Math.round(r * 255)}, ${Math.round(g * 255)}, ${Math.round(b * 255)})`;
        ctx.fill();
        ctx.strokeStyle = "rgba(0,0,0,0.2)";
        ctx.lineWidth = 0.5;
        ctx.stroke();
      }

      // Indicator badge for software mode
      ctx.fillStyle = "rgba(15, 23, 42, 0.75)";
      ctx.fillRect(8, h - 26, 175, 18);
      ctx.fillStyle = "#38bdf8";
      ctx.font = "10px monospace";
      ctx.fillText("✦ 3D Software Fallback Engine", 14, h - 14);
    }
  }

  window.WalkthroughRenderer = WalkthroughRenderer;
})();
