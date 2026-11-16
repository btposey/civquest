// CivQuest Blueprint Studio - HTML5 Canvas Isometric 2.5D Voxel Renderer
// Implements depth sorting, directional shading (top/left/right), and dynamic mouseover X-Ray wall ghosting.

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

    this.canvas.addEventListener("mousemove", (e) => {
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
    let tileWidth = Math.floor(Math.min(36, (width * 0.75) / spanXZ));
    tileWidth = Math.max(16, Math.min(tileWidth, 42));
    const tileHeight = Math.floor(tileWidth / 2);
    const voxelHeight = Math.floor(tileHeight * 1.25);

    // Compute center origin
    const originX = width / 2 + ((dims.length - dims.width) * tileWidth) / 4;
    const originY = height / 2 - ((dims.width + dims.length) * tileHeight) / 4 + (dims.height * voxelHeight) / 3;

    // Draw Ground Plane / Foundation reference grid at Y = 0
    this.drawGroundGrid(ctx, originX, originY, dims, minY, tileWidth, tileHeight, voxelHeight);

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
      const isoX = originX + (v.x - v.z) * (tileWidth / 2);
      const isoY = originY + (v.x + v.z) * (tileHeight / 2) - (v.y - minY) * voxelHeight;

      // Detect front-facing outer wall or roof block
      const isFrontWall = (v.x === dims.width - 1 || v.z === dims.length - 1);
      const isRoof = (v.y === maxY);
      const isGhostTarget = isXRayActive && (isFrontWall || isRoof);

      const baseColor = this.resolveColor(v.blockState);
      const isGlass = v.blockState.includes("glass");

      ctx.save();
      if (isGhostTarget) {
        // Dynamic X-Ray wall ghosting: drop camera-facing front blocks to ~15% opacity with wireframe
        ctx.globalAlpha = 0.15;
      } else if (isGlass) {
        ctx.globalAlpha = 0.5;
      } else {
        ctx.globalAlpha = 1.0;
      }

      this.drawVoxelCube(ctx, isoX, isoY, tileWidth, tileHeight, voxelHeight, baseColor, isGhostTarget);
      ctx.restore();
    }

    // Overlay Status Legend
    ctx.fillStyle = isXRayActive ? "#38bdf8" : "#94a3b8";
    ctx.font = "11px monospace";
    ctx.textAlign = "left";
    const statusText = isXRayActive
      ? "⚡ Dynamic X-Ray Wall Ghosting ACTIVE (Front Walls @ 15% Opacity)"
      : "✦ Hover cursor across canvas for interactive X-Ray interior ghosting";
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
    const cleanId = blockState.split("[")[0].trim().toLowerCase();
    if (this.blockColors[cleanId]) {
      return this.blockColors[cleanId];
    }
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
