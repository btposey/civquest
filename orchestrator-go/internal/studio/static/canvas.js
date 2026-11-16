// CivQuest Blueprint Studio - HTML5 Canvas Isometric 2.5D Voxel Renderer
// Implements depth sorting, directional shading (top/left/right), dynamic mouseover X-Ray wall ghosting,
// and semi-transparent fat ordinal direction arrows (N, S, W, E) along the ground perimeter.

class IsometricCanvas {
  constructor(canvasId) {
    this.canvas = document.getElementById(canvasId);
    if (!this.canvas) return;
    this.ctx = this.canvas.getContext("2d");
    this.blueprint = null;
    this.isHovering = false;
    this.xRayLocked = false;
    this.activeY = 0;

    // Standard block color lookup
    this.blockColors = {
      "minecraft:oak_planks": "#c68a4c",
      "minecraft:spruce_planks": "#79553a",
      "minecraft:birch_planks": "#d7c59a",
      "minecraft:jungle_planks": "#ab7956",
      "minecraft:acacia_planks": "#a85a32",
      "minecraft:dark_oak_planks": "#432b15",
      "minecraft:mangrove_planks": "#763631",
      "minecraft:cherry_planks": "#e4a7a8",
      "minecraft:bamboo_planks": "#c2aa4d",
      "minecraft:oak_log": "#6d5032",
      "minecraft:spruce_log": "#3e2817",
      "minecraft:birch_log": "#ded9ce",
      "minecraft:dark_oak_log": "#362411",
      "minecraft:stone": "#808080",
      "minecraft:cobblestone": "#6b6b6b",
      "minecraft:stone_bricks": "#787878",
      "minecraft:deepslate": "#3b3b44",
      "minecraft:deepslate_bricks": "#2a2a32",
      "minecraft:cobbled_deepslate": "#34343d",
      "minecraft:sandstone": "#ded598",
      "minecraft:brick": "#964332",
      "minecraft:glass": "#a5d8f3",
      "minecraft:glass_pane": "#a5d8f3",
      "minecraft:oak_stairs": "#c68a4c",
      "minecraft:spruce_stairs": "#79553a",
      "minecraft:stone_brick_stairs": "#787878",
      "minecraft:torch": "#f39c12",
      "minecraft:lantern": "#f1c40f",
      "minecraft:barrel": "#87572b",
      "minecraft:chest": "#9e6828",
      "minecraft:crafting_table": "#a37340",
      "minecraft:furnace": "#545454",
      "minecraft:red_bed": "#c0392b",
      "minecraft:oak_door": "#8d6439",
      "minecraft:spruce_door": "#684826",
      "minecraft:ladder": "#b28a50",
      "minecraft:hay_block": "#a59728"
    };

    this.initEvents();
  }

  initEvents() {
    this.canvas.addEventListener("mouseenter", () => {
      this.isHovering = true;
      this.render();
    });

    this.canvas.addEventListener("mouseleave", () => {
      this.isHovering = false;
      this.render();
    });

    this.canvas.addEventListener("mousemove", () => {
      if (!this.isHovering) {
        this.isHovering = true;
        this.render();
      }
    });
  }

  setBlueprint(bp, activeY = 0) {
    this.blueprint = bp;
    this.activeY = activeY;
    this.render();
  }

  toggleXRay() {
    this.xRayLocked = !this.xRayLocked;
    this.render();
    return this.xRayLocked;
  }

  render() {
    if (!this.canvas || !this.ctx) return;
    const ctx = this.ctx;
    const width = this.canvas.width;
    const height = this.canvas.height;

    // Clear background
    ctx.clearRect(0, 0, width, height);

    if (!this.blueprint || !this.blueprint.dimensions) {
      ctx.fillStyle = "#64748b";
      ctx.font = "14px sans-serif";
      ctx.textAlign = "center";
      ctx.fillText("No blueprint loaded. Select or draft a structure.", width / 2, height / 2);
      return;
    }

    const dims = this.blueprint.dimensions;
    const minY = dims.min_y !== undefined ? dims.min_y : 0;
    const maxY = minY + dims.height - 1;

    // Calculate dynamic tile sizes based on bounding box
    const spanXZ = Math.max(dims.width + dims.length, 6);
    let tileWidth = Math.floor(Math.min(36, (width * 0.70) / spanXZ));
    tileWidth = Math.max(16, Math.min(tileWidth, 42));
    const tileHeight = Math.floor(tileWidth / 2);
    const voxelHeight = Math.floor(tileHeight * 1.25);

    // Compute center origin
    const originX = width / 2 + ((dims.length - dims.width) * tileWidth) / 4;
    const originY = height / 2 - ((dims.width + dims.length) * tileHeight) / 4 + (dims.height * voxelHeight) / 3;

    // 1. Draw Ground Plane grid at Y = 0
    this.drawGroundGrid(ctx, originX, originY, dims, minY, tileWidth, tileHeight, voxelHeight);

    // 2. Draw Semi-Transparent Fat Ordinal Direction Arrows (N, S, W, E) on Ground Plane
    this.drawGroundPerimeterArrows(ctx, originX, originY, dims, minY, tileWidth, tileHeight, voxelHeight);

    // Collect all voxels
    const voxels = [];
    const palette = this.blueprint.palette || {};

    if (this.blueprint.layers && this.blueprint.layers.length > 0) {
      for (const layer of this.blueprint.layers) {
        const y = layer.y;
        if (!layer.matrix) continue;
        for (let z = 0; z < layer.matrix.length; z++) {
          const row = layer.matrix[z];
          for (let x = 0; x < row.length; x++) {
            const palIdx = row[x];
            if (palIdx > 0 && palette[palIdx]) {
              voxels.push({
                x,
                y,
                z,
                blockState: palette[palIdx],
                palIdx
              });
            }
          }
        }
      }
    } else if (this.blueprint.blocks && this.blueprint.blocks.length > 0) {
      for (const blk of this.blueprint.blocks) {
        voxels.push({
          x: blk.x,
          y: blk.y,
          z: blk.z,
          blockState: blk.state || "minecraft:stone"
        });
      }
    }

    // Depth Sorting: Painter's Algorithm
    // 1. Y ascending (bottom-to-top)
    // 2. (X + Z) ascending (furthest from camera to closest)
    // 3. X ascending
    voxels.sort((a, b) => {
      if (a.y !== b.y) return a.y - b.y;
      const distA = a.x + a.z;
      const distB = b.x + b.z;
      if (distA !== distB) return distA - distB;
      return a.x - b.x;
    });

    const isXRayActive = this.isHovering || this.xRayLocked;

    // Render sorted voxels
    for (const v of voxels) {
      // Detect front-facing outer wall or roof block
      const isFrontWall = (v.x === dims.width - 1 || v.z === dims.length - 1);
      const isRoof = (v.y === maxY) ||
                     (v.y >= maxY - 1 && (v.blockState.includes("stairs") || v.blockState.includes("slab"))) ||
                     (v.y >= minY + dims.height - 1);

      // In X-Ray mode, completely hide front walls and roof elements to leave interior unobstructed
      if (isXRayActive && (isFrontWall || isRoof)) {
        continue;
      }

      const isoX = originX + (v.x - v.z) * (tileWidth / 2);
      const isoY = originY + (v.x + v.z) * (tileHeight / 2) - (v.y - minY) * voxelHeight;

      const baseColor = this.resolveColor(v.blockState);
      const isGlass = v.blockState.includes("glass");

      ctx.save();
      if (isGlass) {
        ctx.globalAlpha = 0.55;
      } else {
        ctx.globalAlpha = 1.0;
      }

      this.drawVoxelCube(ctx, isoX, isoY, tileWidth, tileHeight, voxelHeight, baseColor, false);
      ctx.restore();
    }

    // 3. Draw Corner Isometric Compass HUD
    this.drawCompassWidget(ctx, width - 48, 48);

    // Overlay Status Legend
    ctx.fillStyle = isXRayActive ? "#38bdf8" : "#94a3b8";
    ctx.font = "11px monospace";
    ctx.textAlign = "left";
    const statusText = isXRayActive
      ? "⚡ Dynamic X-Ray Interior View ACTIVE (Front Walls & Roof Hidden)"
      : "✦ Hover cursor across canvas for interactive X-Ray interior view";
    ctx.fillText(statusText, 14, height - 14);

    if (minY < 0) {
      ctx.fillStyle = "#f59e0b";
      ctx.textAlign = "right";
      ctx.fillText(`⛏ Subterranean Basement Enabled (Min Y = ${minY})`, width - 14, height - 14);
    }
  }

  drawGroundGrid(ctx, originX, originY, dims, minY, tileWidth, tileHeight, voxelHeight) {
    ctx.save();
    ctx.strokeStyle = "rgba(71, 85, 105, 0.4)";
    ctx.lineWidth = 1;

    // Calculate Y=0 plane offset relative to minY
    const groundIsoOffset = (0 - minY) * voxelHeight;

    for (let x = 0; x <= dims.width; x++) {
      const startX = originX + (x - 0) * (tileWidth / 2);
      const startY = originY + (x + 0) * (tileHeight / 2) - groundIsoOffset;
      const endX = originX + (x - dims.length) * (tileWidth / 2);
      const endY = originY + (x + dims.length) * (tileHeight / 2) - groundIsoOffset;

      ctx.beginPath();
      ctx.moveTo(startX, startY);
      ctx.lineTo(endX, endY);
      ctx.stroke();
    }

    for (let z = 0; z <= dims.length; z++) {
      const startX = originX + (0 - z) * (tileWidth / 2);
      const startY = originY + (0 + z) * (tileHeight / 2) - groundIsoOffset;
      const endX = originX + (dims.width - z) * (tileWidth / 2);
      const endY = originY + (dims.width + z) * (tileHeight / 2) - groundIsoOffset;

      ctx.beginPath();
      ctx.moveTo(startX, startY);
      ctx.lineTo(endX, endY);
      ctx.stroke();
    }
    ctx.restore();
  }

  // Draw semi-transparent fat arrows with bold 'N', 'S', 'W', 'E' labels around ground perimeter
  drawGroundPerimeterArrows(ctx, originX, originY, dims, minY, tileWidth, tileHeight, voxelHeight) {
    ctx.save();

    const hw = tileWidth / 2;
    const hh = tileHeight / 2;
    const groundIsoOffset = (0 - minY) * voxelHeight;

    // North: -Z direction -> screen vector (+hw, -hh), angle = Math.atan2(-hh, hw)
    const nAngle = Math.atan2(-hh, hw);
    const nMidX = originX + (dims.width / 2.0 - 0) * hw;
    const nMidY = originY + (dims.width / 2.0 + 0) * hh - groundIsoOffset;
    const nDist = Math.max(26, hw * 1.15);
    const nPosX = nMidX + Math.cos(nAngle) * nDist;
    const nPosY = nMidY + Math.sin(nAngle) * nDist;
    this.renderFatArrow(ctx, nPosX, nPosY, nAngle, "N", "rgba(56, 189, 248, 0.55)", "rgba(56, 189, 248, 0.95)");

    // South: +Z direction -> screen vector (-hw, +hh), angle = Math.atan2(hh, -hw)
    const sAngle = Math.atan2(hh, -hw);
    const sMidX = originX + (dims.width / 2.0 - dims.length) * hw;
    const sMidY = originY + (dims.width / 2.0 + dims.length) * hh - groundIsoOffset;
    const sDist = Math.max(26, hw * 1.15);
    const sPosX = sMidX + Math.cos(sAngle) * sDist;
    const sPosY = sMidY + Math.sin(sAngle) * sDist;
    this.renderFatArrow(ctx, sPosX, sPosY, sAngle, "S", "rgba(56, 189, 248, 0.55)", "rgba(56, 189, 248, 0.95)");

    // East: +X direction -> screen vector (+hw, +hh), angle = Math.atan2(hh, hw)
    const eAngle = Math.atan2(hh, hw);
    const eMidX = originX + (dims.width - dims.length / 2.0) * hw;
    const eMidY = originY + (dims.width + dims.length / 2.0) * hh - groundIsoOffset;
    const eDist = Math.max(26, hw * 1.15);
    const ePosX = eMidX + Math.cos(eAngle) * eDist;
    const ePosY = eMidY + Math.sin(eAngle) * eDist;
    this.renderFatArrow(ctx, ePosX, ePosY, eAngle, "E", "rgba(56, 189, 248, 0.55)", "rgba(56, 189, 248, 0.95)");

    // West: -X direction -> screen vector (-hw, -hh), angle = Math.atan2(-hh, -hw)
    const wAngle = Math.atan2(-hh, -hw);
    const wMidX = originX + (0 - dims.length / 2.0) * hw;
    const wMidY = originY + (0 + dims.length / 2.0) * hh - groundIsoOffset;
    const wDist = Math.max(26, hw * 1.15);
    const wPosX = wMidX + Math.cos(wAngle) * wDist;
    const wPosY = wMidY + Math.sin(wAngle) * wDist;
    this.renderFatArrow(ctx, wPosX, wPosY, wAngle, "W", "rgba(56, 189, 248, 0.55)", "rgba(56, 189, 248, 0.95)");

    ctx.restore();
  }

  // Helper to render a stylized semi-transparent fat arrow with bold label
  renderFatArrow(ctx, x, y, angle, label, fillColor, strokeColor) {
    ctx.save();
    ctx.translate(x, y);

    // Draw rotated fat arrow
    ctx.save();
    ctx.rotate(angle);
    ctx.beginPath();
    ctx.moveTo(18, 0);       // Tip
    ctx.lineTo(6, -9);       // Top barb
    ctx.lineTo(6, -4);       // Top notch
    ctx.lineTo(-12, -4);     // Top rear
    ctx.lineTo(-12, 4);      // Bottom rear
    ctx.lineTo(6, 4);        // Bottom notch
    ctx.lineTo(6, 9);        // Bottom barb
    ctx.closePath();

    ctx.fillStyle = fillColor;
    ctx.fill();
    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = 1.6;
    ctx.stroke();
    ctx.restore();

    // Draw upright bold ordinal letter label with glowing pill badge
    ctx.beginPath();
    ctx.arc(0, 0, 9, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(15, 23, 42, 0.85)";
    ctx.fill();
    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = 1.2;
    ctx.stroke();

    ctx.fillStyle = "#ffffff";
    ctx.font = "bold 11px sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText(label, 0, 0);

    ctx.restore();
  }

  // Draw miniature isometric compass widget in canvas corner
  drawCompassWidget(ctx, cx, cy) {
    ctx.save();
    ctx.beginPath();
    ctx.arc(cx, cy, 26, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(15, 23, 42, 0.75)";
    ctx.fill();
    ctx.strokeStyle = "rgba(56, 189, 248, 0.35)";
    ctx.lineWidth = 1;
    ctx.stroke();

    const hw = 16;
    const hh = 8;
    const dirs = [
      { label: "N", angle: Math.atan2(-hh, hw), color: "#38bdf8" },
      { label: "S", angle: Math.atan2(hh, -hw), color: "#94a3b8" },
      { label: "E", angle: Math.atan2(hh, hw), color: "#94a3b8" },
      { label: "W", angle: Math.atan2(-hh, -hw), color: "#94a3b8" }
    ];

    for (const d of dirs) {
      const px = cx + Math.cos(d.angle) * 17;
      const py = cy + Math.sin(d.angle) * 17;

      ctx.beginPath();
      ctx.moveTo(cx, cy);
      ctx.lineTo(px, py);
      ctx.strokeStyle = d.color;
      ctx.lineWidth = d.label === "N" ? 2 : 1;
      ctx.stroke();

      ctx.fillStyle = d.color;
      ctx.font = "bold 9px sans-serif";
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.fillText(d.label, cx + Math.cos(d.angle) * 22, cy + Math.sin(d.angle) * 22);
    }

    ctx.restore();
  }

  drawVoxelCube(ctx, isoX, isoY, tileWidth, tileHeight, voxelHeight, baseColor, isGhost) {
    const hw = tileWidth / 2;
    const hh = tileHeight / 2;

    const topShade = this.adjustColor(baseColor, 20);
    const leftShade = this.adjustColor(baseColor, -12);
    const rightShade = this.adjustColor(baseColor, -26);

    // 1. Top Face (Rhombus)
    ctx.beginPath();
    ctx.moveTo(isoX, isoY);
    ctx.lineTo(isoX + hw, isoY + hh);
    ctx.lineTo(isoX, isoY + tileHeight);
    ctx.lineTo(isoX - hw, isoY + hh);
    ctx.closePath();
    ctx.fillStyle = topShade;
    ctx.fill();
    ctx.strokeStyle = isGhost ? "rgba(255, 255, 255, 0.45)" : "rgba(0, 0, 0, 0.35)";
    ctx.lineWidth = isGhost ? 1.2 : 0.8;
    ctx.stroke();

    // 2. Left Face
    ctx.beginPath();
    ctx.moveTo(isoX - hw, isoY + hh);
    ctx.lineTo(isoX, isoY + tileHeight);
    ctx.lineTo(isoX, isoY + tileHeight + voxelHeight);
    ctx.lineTo(isoX - hw, isoY + hh + voxelHeight);
    ctx.closePath();
    ctx.fillStyle = leftShade;
    ctx.fill();
    ctx.stroke();

    // 3. Right Face
    ctx.beginPath();
    ctx.moveTo(isoX, isoY + tileHeight);
    ctx.lineTo(isoX + hw, isoY + hh);
    ctx.lineTo(isoX + hw, isoY + hh + voxelHeight);
    ctx.lineTo(isoX, isoY + tileHeight + voxelHeight);
    ctx.closePath();
    ctx.fillStyle = rightShade;
    ctx.fill();
    ctx.stroke();
  }

  resolveColor(blockState) {
    if (!blockState) return "#808080";
    let cleanId = blockState.split("[")[0].trim().toLowerCase();
    if (cleanId.startsWith("#")) cleanId = cleanId.substring(1);

    if (this.blockColors[cleanId]) {
      return this.blockColors[cleanId];
    }

    // Taxonomy tag fallbacks
    if (cleanId.includes("plank")) return "#c68a4c";
    if (cleanId.includes("log") || cleanId.includes("wood")) return "#6d5032";
    if (cleanId.includes("stair")) return "#c68a4c";
    if (cleanId.includes("slab")) return "#c68a4c";
    if (cleanId.includes("door") || cleanId.includes("trapdoor") || cleanId.includes("fence")) return "#8d6439";
    if (cleanId.includes("cobble")) return "#6b6b6b";
    if (cleanId.includes("stone_brick")) return "#787878";
    if (cleanId.includes("stone")) return "#808080";
    if (cleanId.includes("deepslate")) return "#3b3b44";
    if (cleanId.includes("glass")) return "#a5d8f3";
    if (cleanId.includes("torch") || cleanId.includes("light")) return "#f39c12";
    if (cleanId.includes("bed")) return "#c0392b";
    if (cleanId.includes("chest") || cleanId.includes("barrel")) return "#87572b";

    // Deterministic hash color fallback
    let hash = 0;
    for (let i = 0; i < cleanId.length; i++) {
      hash = cleanId.charCodeAt(i) + ((hash << 5) - hash);
    }
    const h = Math.abs(hash % 360);
    return `hsl(${h}, 45%, 45%)`;
  }

  adjustColor(color, percent) {
    if (color.startsWith("hsl")) {
      return color;
    }
    let num = parseInt(color.replace("#", ""), 16);
    if (isNaN(num)) return color;
    let r = (num >> 16) + Math.round(2.55 * percent);
    let g = ((num >> 8) & 0x00ff) + Math.round(2.55 * percent);
    let b = (num & 0x0000ff) + Math.round(2.55 * percent);

    r = Math.min(255, Math.max(0, r));
    g = Math.min(255, Math.max(0, g));
    b = Math.min(255, Math.max(0, b));

    return `rgb(${r}, ${g}, ${b})`;
  }
}

window.IsometricCanvas = IsometricCanvas;
