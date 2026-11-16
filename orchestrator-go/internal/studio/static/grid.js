// CivQuest Blueprint Studio - 2D Layer Matrix Grid Editor
// Provides horizontal ground plane grid editing with signed elevation stepper (including subterranean Y < 0),
// onion-skinning ghost guides for Y-1 layer, orientation controls with 'R' hotkey, and real-time BOM calculation.

class LayerGridEditor {
  constructor(containerId, options = {}) {
    this.container = document.getElementById(containerId);
    this.blueprint = null;
    this.activeY = 0;
    this.onionSkinning = true;
    this.selectedBlockId = "minecraft:oak_planks";
    this.orientation = {
      facing: "north",
      axis: "y",
      half: "bottom",
      rotation: 0
    };
    this.activeTool = "place"; // "place" | "erase"
    this.onUpdateCallback = options.onUpdate || (() => {});

    this.initKeyboard();
  }

  initKeyboard() {
    window.addEventListener("keydown", (e) => {
      // Ignore if typing inside text fields or textareas
      if (e.target.tagName === "INPUT" || e.target.tagName === "TEXTAREA" || e.target.isContentEditable) {
        return;
      }
      if (e.key === "r" || e.key === "R") {
        e.preventDefault();
        this.rotateClockwise90();
      }
    });
  }

  setBlueprint(bp) {
    this.blueprint = bp;
    if (bp && bp.dimensions) {
      const minY = bp.dimensions.min_y !== undefined ? bp.dimensions.min_y : 0;
      // Default to ground level Y = 0 if within bounds, else minY
      if (0 >= minY && 0 < minY + bp.dimensions.height) {
        this.activeY = 0;
      } else {
        this.activeY = minY;
      }
    } else {
      this.activeY = 0;
    }
    this.render();
  }

  setActiveY(y) {
    if (!this.blueprint || !this.blueprint.dimensions) return;
    const dims = this.blueprint.dimensions;
    const minY = dims.min_y !== undefined ? dims.min_y : 0;
    const maxY = minY + dims.height - 1;
    this.activeY = Math.max(minY, Math.min(y, maxY));
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  stepElevation(delta) {
    if (!this.blueprint || !this.blueprint.dimensions) return;
    this.setActiveY(this.activeY + delta);
  }

  addBasementLayer() {
    if (!this.blueprint) return;
    const dims = this.blueprint.dimensions;
    const currentMinY = dims.min_y !== undefined ? dims.min_y : 0;
    const newMinY = currentMinY - 1;

    // Create empty matrix for the new subterranean basement layer
    const matrix = [];
    for (let z = 0; z < dims.length; z++) {
      matrix.push(new Array(dims.width).fill(0));
    }

    if (!this.blueprint.layers) this.blueprint.layers = [];
    this.blueprint.layers.unshift({
      y: newMinY,
      matrix: matrix
    });

    dims.min_y = newMinY;
    dims.height += 1;
    this.activeY = newMinY;

    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  addLayerAbove() {
    if (!this.blueprint) return;
    const dims = this.blueprint.dimensions;
    const currentMinY = dims.min_y !== undefined ? dims.min_y : 0;
    const newTopY = currentMinY + dims.height;

    const matrix = [];
    for (let z = 0; z < dims.length; z++) {
      matrix.push(new Array(dims.width).fill(0));
    }

    if (!this.blueprint.layers) this.blueprint.layers = [];
    this.blueprint.layers.push({
      y: newTopY,
      matrix: matrix
    });

    dims.height += 1;
    this.activeY = newTopY;

    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  toggleOnionSkinning() {
    this.onionSkinning = !this.onionSkinning;
    this.render();
    return this.onionSkinning;
  }

  rotateClockwise90() {
    // 1. Facing: north -> east -> south -> west -> north
    const facings = ["north", "east", "south", "west"];
    const idx = facings.indexOf(this.orientation.facing);
    this.orientation.facing = facings[(idx + 1) % facings.length];

    // 2. Axis: x -> z -> x
    if (this.orientation.axis === "x") {
      this.orientation.axis = "z";
    } else if (this.orientation.axis === "z") {
      this.orientation.axis = "x";
    }

    // 3. Rotation (0-15): 90 degrees is +4 steps
    this.orientation.rotation = (this.orientation.rotation + 4) % 16;

    // Dispatch DOM update event for UI controls
    const event = new CustomEvent("orientation-changed", { detail: this.orientation });
    window.dispatchEvent(event);

    this.showRotationFlash();
  }

  showRotationFlash() {
    const badge = document.getElementById("orientation-indicator");
    if (badge) {
      badge.textContent = `Facing: ${this.orientation.facing.toUpperCase()} | Axis: ${this.orientation.axis} [R]`;
      badge.classList.add("flash");
      setTimeout(() => badge.classList.remove("flash"), 400);
    }
  }

  getEffectiveBlockState() {
    const baseId = this.selectedBlockId;
    const cat = this.getOrientationCategory(baseId);

    switch (cat) {
      case "axis":
        return `${baseId}[axis=${this.orientation.axis}]`;
      case "horizontal":
        return `${baseId}[facing=${this.orientation.facing}]`;
      case "slab":
        return `${baseId}[type=${this.orientation.half}]`;
      case "rotation":
        return `${baseId}[rotation=${this.orientation.rotation}]`;
      case "6way":
        return `${baseId}[facing=${this.orientation.facing}]`;
      default:
        return baseId;
    }
  }

  getOrientationCategory(blockId) {
    if (!blockId) return "none";
    if (blockId.includes("_log") || blockId.includes("pillar") || blockId.includes("basalt") || blockId.includes("hay_block")) {
      return "axis";
    }
    if (blockId.includes("_stairs") || blockId.includes("_door") || blockId.includes("_fence_gate")) {
      return "horizontal";
    }
    if (blockId.includes("_slab")) {
      return "slab";
    }
    if (blockId.includes("sign") || blockId.includes("banner")) {
      return "rotation";
    }
    if (blockId.includes("piston") || blockId.includes("dispenser") || blockId.includes("dropper") || blockId.includes("observer")) {
      return "6way";
    }
    return "none";
  }

  render() {
    if (!this.container) return;
    this.container.innerHTML = "";

    if (!this.blueprint || !this.blueprint.dimensions) {
      this.container.innerHTML = '<p class="placeholder-text">Load or draft a blueprint to activate the 2D layer grid editor.</p>';
      return;
    }

    const dims = this.blueprint.dimensions;
    const currentLayer = this.ensureLayer(this.activeY);
    const prevLayer = this.onionSkinning ? this.findLayer(this.activeY - 1) : null;
    const palette = this.blueprint.palette || {};

    // Grid Wrapper with X and Z coordinate rulers
    const gridWrap = document.createElement("div");
    gridWrap.className = "layer-grid-wrapper";

    const table = document.createElement("table");
    table.className = "layer-grid-table";
    table.addEventListener("contextmenu", (e) => e.preventDefault());

    // Header Row (X Axis coordinates)
    const headerRow = document.createElement("tr");
    const cornerTh = document.createElement("th");
    cornerTh.className = "grid-axis-corner";
    cornerTh.textContent = "Z \\ X";
    headerRow.appendChild(cornerTh);

    for (let x = 0; x < dims.width; x++) {
      const th = document.createElement("th");
      th.className = "grid-axis-label";
      th.textContent = x;
      headerRow.appendChild(th);
    }
    table.appendChild(headerRow);

    // Matrix Rows (Z Axis)
    for (let z = 0; z < dims.length; z++) {
      const tr = document.createElement("tr");
      const zLabel = document.createElement("th");
      zLabel.className = "grid-axis-label";
      zLabel.textContent = z;
      tr.appendChild(zLabel);

      for (let x = 0; x < dims.width; x++) {
        const td = document.createElement("td");
        td.className = "grid-cell";
        td.dataset.x = x;
        td.dataset.z = z;

        const palIdx = currentLayer.matrix[z][x];
        const hasBlock = palIdx > 0 && palette[palIdx];

        if (hasBlock) {
          const state = palette[palIdx];
          const shortName = this.formatShortBlockName(state);
          td.classList.add("grid-cell-filled");
          td.style.backgroundColor = this.resolveCellColor(state);
          td.textContent = shortName;
          td.title = `(${x}, ${this.activeY}, ${z}): ${state} [Click to change, Right-click to erase]`;
        } else {
          // Check Onion Skinning: Underneath layer Y-1
          let hasGhostGuide = false;
          if (prevLayer && prevLayer.matrix && prevLayer.matrix[z] && prevLayer.matrix[z][x] > 0) {
            const ghostPalIdx = prevLayer.matrix[z][x];
            const ghostState = palette[ghostPalIdx];
            if (ghostState) {
              hasGhostGuide = true;
              td.classList.add("grid-cell-onion");
              td.textContent = "👻 " + this.formatShortBlockName(ghostState);
              td.title = `Ghost Guide [Layer Y=${this.activeY - 1}]: ${ghostState}`;
            }
          }

          if (!hasGhostGuide) {
            td.classList.add("grid-cell-empty");
            td.textContent = "";
            td.title = `(${x}, ${this.activeY}, ${z}): air [Click to place ${this.selectedBlockId}]`;
          }
        }

        // Cell interactions
        td.addEventListener("mousedown", (e) => {
          if (e.button === 0) {
            // Left Click
            if (this.activeTool === "erase") {
              this.eraseBlock(x, z);
            } else {
              this.placeBlock(x, z);
            }
          } else if (e.button === 2) {
            // Right Click = Erase
            e.preventDefault();
            this.eraseBlock(x, z);
          }
        });

        td.addEventListener("mouseenter", () => {
          this.updateCursorReadout(x, z);
        });

        tr.appendChild(td);
      }
      table.appendChild(tr);
    }

    gridWrap.appendChild(table);
    this.container.appendChild(gridWrap);

    // Update subterranean badge visibility
    this.updateSubterraneanBadge();
  }

  ensureLayer(y) {
    if (!this.blueprint.layers) {
      this.blueprint.layers = [];
    }
    let layer = this.blueprint.layers.find(l => l.y === y);
    if (!layer) {
      const dims = this.blueprint.dimensions;
      const matrix = [];
      for (let z = 0; z < dims.length; z++) {
        matrix.push(new Array(dims.width).fill(0));
      }
      layer = { y, matrix };
      this.blueprint.layers.push(layer);
      this.blueprint.layers.sort((a, b) => a.y - b.y);
    }
    return layer;
  }

  findLayer(y) {
    if (!this.blueprint || !this.blueprint.layers) return null;
    return this.blueprint.layers.find(l => l.y === y) || null;
  }

  placeBlock(x, z) {
    const layer = this.ensureLayer(this.activeY);
    const blockState = this.getEffectiveBlockState();

    if (!this.blueprint.palette) {
      this.blueprint.palette = {};
    }

    // Find existing palette index or allocate new one
    let targetIdx = 0;
    for (const [idxStr, state] of Object.entries(this.blueprint.palette)) {
      if (state === blockState) {
        targetIdx = parseInt(idxStr, 10);
        break;
      }
    }

    if (targetIdx === 0) {
      let maxIdx = 0;
      for (const idxStr of Object.keys(this.blueprint.palette)) {
        const val = parseInt(idxStr, 10);
        if (!isNaN(val) && val > maxIdx) maxIdx = val;
      }
      targetIdx = maxIdx + 1;
      this.blueprint.palette[targetIdx] = blockState;
    }

    layer.matrix[z][x] = targetIdx;
    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  eraseBlock(x, z) {
    const layer = this.ensureLayer(this.activeY);
    layer.matrix[z][x] = 0;
    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  recalculateStats() {
    if (!this.blueprint) return;
    const bom = {};
    let totalBlocks = 0;
    let excavation = 0;

    const dims = this.blueprint.dimensions;
    const palette = this.blueprint.palette || {};

    if (this.blueprint.layers) {
      for (const layer of this.blueprint.layers) {
        const isSubterranean = layer.y < 0;
        if (!layer.matrix) continue;
        for (let z = 0; z < layer.matrix.length; z++) {
          const row = layer.matrix[z];
          for (let x = 0; x < row.length; x++) {
            const idx = row[x];
            if (idx > 0 && palette[idx]) {
              const state = palette[idx];
              bom[state] = (bom[state] || 0) + 1;
              totalBlocks++;
            }
            if (isSubterranean) {
              // Subterranean excavation volume includes all hollow and interior volume
              excavation++;
            }
          }
        }
      }
    }

    this.blueprint.bom = bom;
    this.blueprint.excavation = excavation;

    // Real-time DOM stats update
    const totalBlocksEl = document.getElementById("stat-total-blocks");
    if (totalBlocksEl) totalBlocksEl.textContent = totalBlocks;

    const excavationEl = document.getElementById("stat-excavation");
    if (excavationEl) excavationEl.textContent = excavation;

    this.renderBOMList(bom);
  }

  renderBOMList(bom) {
    const container = document.getElementById("realtime-bom-list");
    if (!container) return;
    container.innerHTML = "";

    const entries = Object.entries(bom || {});
    if (entries.length === 0) {
      container.innerHTML = '<span class="placeholder-text" style="font-size:0.8rem">No blocks placed</span>';
      return;
    }

    entries.sort((a, b) => b[1] - a[1]);
    for (const [state, count] of entries) {
      const item = document.createElement("div");
      item.className = "bom-item-chip";
      item.innerHTML = `<code>${this.escapeHTML(state)}</code>: <strong>${count}</strong>`;
      container.appendChild(item);
    }
  }

  updateSubterraneanBadge() {
    const badge = document.getElementById("subterranean-badge");
    const indicator = document.getElementById("layer-indicator");

    if (indicator) {
      indicator.textContent = `Layer Y = ${this.activeY}`;
    }

    if (badge) {
      if (this.activeY < 0) {
        badge.classList.remove("hidden");
        badge.textContent = `⛏ Subterranean Basement Layer (Y = ${this.activeY}) • Excavation Active`;
      } else {
        badge.classList.add("hidden");
      }
    }
  }

  updateCursorReadout(x, z) {
    const readout = document.getElementById("grid-cursor-readout");
    if (readout) {
      const layer = this.findLayer(this.activeY);
      const val = layer && layer.matrix && layer.matrix[z] ? layer.matrix[z][x] : 0;
      const state = val > 0 && this.blueprint.palette ? this.blueprint.palette[val] : "air";
      readout.textContent = `Cursor: X=${x}, Z=${z}, Y=${this.activeY} [${state}]`;
    }
  }

  formatShortBlockName(state) {
    if (!state) return "";
    const clean = state.split("[")[0].replace("minecraft:", "").replace("#minecraft:", "");
    const parts = clean.split("_");
    if (parts.length > 2) {
      return parts.map(p => p[0].toUpperCase()).join("");
    }
    return parts.map(p => p.slice(0, 3)).join(" ");
  }

  resolveCellColor(state) {
    if (!state) return "#334155";
    const clean = state.split("[")[0].replace("minecraft:", "");
    if (clean.includes("plank")) return "#7c5228";
    if (clean.includes("log")) return "#4a3219";
    if (clean.includes("stone") || clean.includes("cobble")) return "#52525b";
    if (clean.includes("deepslate")) return "#27272a";
    if (clean.includes("glass")) return "#0369a1";
    if (clean.includes("brick")) return "#7f1d1d";
    if (clean.includes("torch") || clean.includes("lantern")) return "#b45309";
    if (clean.includes("barrel") || clean.includes("chest")) return "#854d0e";
    return "#3f3f46";
  }

  escapeHTML(str) {
    return String(str || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }
}

window.LayerGridEditor = LayerGridEditor;
