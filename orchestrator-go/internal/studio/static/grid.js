// CivQuest Blueprint Studio - 2D Layer Matrix Grid Editor
// Provides horizontal ground plane grid editing with signed elevation stepper (including subterranean Y < 0),
// onion-skinning ghost guides for Y-1 layer, orientation controls with 'R' hotkey,
// semi-transparent fat ordinal direction arrows (N, S, W, E), and real-time BOM calculation.

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

    // Dispatch DOM update event for UI controls & block preview
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
    const clean = blockId.toLowerCase();
    if (clean.includes("_log") || clean.includes("pillar") || clean.includes("basalt") || clean.includes("hay_block") || clean.includes(":logs")) {
      return "axis";
    }
    if (clean.includes("_stairs") || clean.includes("_door") || clean.includes("_fence_gate") || clean.includes(":stairs") || clean.includes(":doors")) {
      return "horizontal";
    }
    if (clean.includes("_slab") || clean.includes(":slabs")) {
      return "slab";
    }
    if (clean.includes("sign") || clean.includes("banner")) {
      return "rotation";
    }
    if (clean.includes("piston") || clean.includes("dispenser") || clean.includes("dropper") || clean.includes("observer")) {
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

    // Grid Wrapper with Ordinal NSWE Direction Indicators
    const gridWrap = document.createElement("div");
    gridWrap.className = "layer-grid-wrapper";

    // NORTH Indicator Bar (Top: -Z)
    const northBar = document.createElement("div");
    northBar.className = "grid-ordinal-bar ordinal-north";
    northBar.innerHTML = `<span class="ordinal-fat-arrow">▲</span> <strong class="ordinal-bold-label">N</strong> <span class="ordinal-sub">NORTH (-Z)</span>`;
    gridWrap.appendChild(northBar);

    const gridCenterRow = document.createElement("div");
    gridCenterRow.className = "grid-middle-row";

    // WEST Indicator Bar (Left: -X)
    const westBar = document.createElement("div");
    westBar.className = "grid-ordinal-bar ordinal-west";
    westBar.innerHTML = `<span class="ordinal-fat-arrow">◄</span><strong class="ordinal-bold-label">W</strong><span class="ordinal-sub">(-X)</span>`;
    gridCenterRow.appendChild(westBar);

    const table = document.createElement("table");
    table.className = "layer-grid-table";
    table.addEventListener("contextmenu", (e) => e.preventDefault());

    // Header Row (X Axis coordinates: West -> East)
    const headerRow = document.createElement("tr");
    const cornerTh = document.createElement("th");
    cornerTh.className = "grid-axis-corner";
    cornerTh.innerHTML = `Z \\ X`;
    headerRow.appendChild(cornerTh);

    for (let x = 0; x < dims.width; x++) {
      const th = document.createElement("th");
      th.className = "grid-axis-label";
      th.textContent = x;
      headerRow.appendChild(th);
    }
    table.appendChild(headerRow);

    // Matrix Rows (Z Axis: North -> South)
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
          td.title = `(${x}, ${this.activeY}, ${z}): ${state} [Click to change, Right-click to erase]`;

          const clean = state.toLowerCase();
          if (clean.includes("bed")) {
            const facing = (state.match(/facing=([a-z]+)/)?.[1]) || "north";
            const isHead = state.includes("part=head");
            const isFoot = state.includes("part=foot");

            if (isHead) {
              td.classList.add("grid-cell-bed-head", `bed-head-${facing}`);
              td.innerHTML = `<span class="bed-pillow-accent"></span><span style="font-size:0.68rem; font-weight:bold; position:relative; z-index:3;">Head</span>`;
            } else if (isFoot) {
              td.classList.add("grid-cell-bed-foot", `bed-foot-${facing}`);
              td.innerHTML = `<span style="font-size:0.68rem; font-weight:bold; position:relative; z-index:3;">Foot</span>`;
            } else {
              td.textContent = shortName;
            }
          } else if (clean.includes("door") && !clean.includes("trapdoor")) {
            const isLower = state.includes("half=lower");
            const isUpper = state.includes("half=upper");
            if (isLower) {
              td.classList.add("grid-cell-door-lower");
              td.innerHTML = `<span style="font-size:0.65rem; font-weight:bold;">🚪 Lower</span>`;
            } else if (isUpper) {
              td.classList.add("grid-cell-door-upper");
              td.innerHTML = `<span style="font-size:0.65rem; font-weight:bold;">🚪 Upper</span>`;
            } else {
              td.textContent = shortName;
            }
          } else {
            td.textContent = shortName;
          }
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

    gridCenterRow.appendChild(table);

    // EAST Indicator Bar (Right: +X)
    const eastBar = document.createElement("div");
    eastBar.className = "grid-ordinal-bar ordinal-east";
    eastBar.innerHTML = `<span class="ordinal-fat-arrow">►</span><strong class="ordinal-bold-label">E</strong><span class="ordinal-sub">(+X)</span>`;
    gridCenterRow.appendChild(eastBar);

    gridWrap.appendChild(gridCenterRow);

    // SOUTH Indicator Bar (Bottom: +Z)
    const southBar = document.createElement("div");
    southBar.className = "grid-ordinal-bar ordinal-south";
    southBar.innerHTML = `<span class="ordinal-fat-arrow">▼</span> <strong class="ordinal-bold-label">S</strong> <span class="ordinal-sub">SOUTH (+Z)</span>`;
    gridWrap.appendChild(southBar);

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

  isBedBlock(blockId) {
    if (!blockId) return false;
    const clean = blockId.toLowerCase();
    return clean.includes("bed") || clean.includes(":beds");
  }

  isDoorBlock(blockId) {
    if (!blockId) return false;
    const clean = blockId.toLowerCase();
    return (clean.includes("door") || clean.includes(":doors")) && !clean.includes("trapdoor");
  }

  getOrAllocatePaletteIndex(blockState) {
    if (!this.blueprint.palette) {
      this.blueprint.palette = {};
    }
    for (const [idxStr, state] of Object.entries(this.blueprint.palette)) {
      if (state === blockState) {
        return parseInt(idxStr, 10);
      }
    }
    let maxIdx = 0;
    for (const idxStr of Object.keys(this.blueprint.palette)) {
      const val = parseInt(idxStr, 10);
      if (!isNaN(val) && val > maxIdx) maxIdx = val;
    }
    const targetIdx = maxIdx + 1;
    this.blueprint.palette[targetIdx] = blockState;
    return targetIdx;
  }

  placeBlock(x, z) {
    const dims = this.blueprint.dimensions;

    // 1. Bed Multi-Block Placement (Occupies 2 horizontal voxels: Foot at (x, z), Head at adjacent)
    if (this.isBedBlock(this.selectedBlockId)) {
      let baseId = this.selectedBlockId;
      if (baseId.startsWith("#") || baseId === "bed") {
        baseId = "minecraft:red_bed";
      }
      const facing = this.orientation.facing;
      let headX = x;
      let headZ = z;

      if (facing === "north") headZ = z - 1;
      else if (facing === "south") headZ = z + 1;
      else if (facing === "west") headX = x - 1;
      else if (facing === "east") headX = x + 1;

      if (headX < 0 || headX >= dims.width || headZ < 0 || headZ >= dims.length) {
        console.warn(`[Studio] Bed placement out of bounds: head (${headX}, ${headZ}) exceeds dimensions ${dims.width}x${dims.length}`);
        return;
      }

      const footState = `${baseId}[facing=${facing},part=foot]`;
      const headState = `${baseId}[facing=${facing},part=head]`;

      const footIdx = this.getOrAllocatePaletteIndex(footState);
      const headIdx = this.getOrAllocatePaletteIndex(headState);

      const layer = this.ensureLayer(this.activeY);
      layer.matrix[z][x] = footIdx;
      layer.matrix[headZ][headX] = headIdx;

      this.recalculateStats();
      this.render();
      this.onUpdateCallback(this.blueprint, this.activeY);
      return;
    }

    // 2. Door Multi-Block Placement (Occupies 2 vertical voxels: Lower at Y, Upper at Y+1)
    if (this.isDoorBlock(this.selectedBlockId)) {
      let baseId = this.selectedBlockId;
      if (baseId.startsWith("#") || baseId === "door") {
        baseId = "minecraft:oak_door";
      }
      const facing = this.orientation.facing;
      const lowerState = `${baseId}[facing=${facing},half=lower]`;
      const upperState = `${baseId}[facing=${facing},half=upper]`;

      const lowerIdx = this.getOrAllocatePaletteIndex(lowerState);
      const upperIdx = this.getOrAllocatePaletteIndex(upperState);

      const minY = dims.min_y !== undefined ? dims.min_y : 0;
      if (this.activeY + 1 >= minY + dims.height) {
        dims.height = (this.activeY + 1 - minY) + 1;
      }

      const layerLower = this.ensureLayer(this.activeY);
      const layerUpper = this.ensureLayer(this.activeY + 1);

      layerLower.matrix[z][x] = lowerIdx;
      layerUpper.matrix[z][x] = upperIdx;

      this.recalculateStats();
      this.render();
      this.onUpdateCallback(this.blueprint, this.activeY);
      return;
    }

    // 3. Standard Single-Voxel Block Placement
    const layer = this.ensureLayer(this.activeY);
    const blockState = this.getEffectiveBlockState();
    const targetIdx = this.getOrAllocatePaletteIndex(blockState);

    layer.matrix[z][x] = targetIdx;
    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  eraseBlock(x, z) {
    const layer = this.ensureLayer(this.activeY);
    const palIdx = layer.matrix[z][x];
    const palette = this.blueprint.palette || {};
    const state = palette[palIdx] || "";
    const clean = state.toLowerCase();

    // 1. Bed partner erase: clearing head or foot automatically clears partner voxel
    if (clean.includes("bed")) {
      let facing = "north";
      const mFacing = state.match(/facing=([a-z]+)/);
      if (mFacing) facing = mFacing[1];

      const isHead = state.includes("part=head");
      const isFoot = state.includes("part=foot");

      let partnerX = x;
      let partnerZ = z;

      if (isFoot) {
        if (facing === "north") partnerZ = z - 1;
        else if (facing === "south") partnerZ = z + 1;
        else if (facing === "west") partnerX = x - 1;
        else if (facing === "east") partnerX = x + 1;
      } else if (isHead) {
        if (facing === "north") partnerZ = z + 1;
        else if (facing === "south") partnerZ = z - 1;
        else if (facing === "west") partnerX = x + 1;
        else if (facing === "east") partnerX = x - 1;
      }

      const dims = this.blueprint.dimensions;
      if (partnerX >= 0 && partnerX < dims.width && partnerZ >= 0 && partnerZ < dims.length) {
        const partnerIdx = layer.matrix[partnerZ][partnerX];
        const partnerState = palette[partnerIdx] || "";
        if (partnerState.toLowerCase().includes("bed")) {
          layer.matrix[partnerZ][partnerX] = 0;
        }
      }
    }

    // 2. Door vertical partner erase: clearing lower or upper clears partner voxel on Y+1 or Y-1
    if (clean.includes("door") && !clean.includes("trapdoor")) {
      const isLower = state.includes("half=lower");
      const isUpper = state.includes("half=upper");

      if (isLower) {
        const upperLayer = this.findLayer(this.activeY + 1);
        if (upperLayer && upperLayer.matrix && upperLayer.matrix[z]) {
          const upperIdx = upperLayer.matrix[z][x];
          const upperState = palette[upperIdx] || "";
          if (upperState.toLowerCase().includes("door")) {
            upperLayer.matrix[z][x] = 0;
          }
        }
      } else if (isUpper) {
        const lowerLayer = this.findLayer(this.activeY - 1);
        if (lowerLayer && lowerLayer.matrix && lowerLayer.matrix[z]) {
          const lowerIdx = lowerLayer.matrix[z][x];
          const lowerState = palette[lowerIdx] || "";
          if (lowerState.toLowerCase().includes("door")) {
            lowerLayer.matrix[z][x] = 0;
          }
        }
      } else {
        for (const delta of [1, -1]) {
          const adjLayer = this.findLayer(this.activeY + delta);
          if (adjLayer && adjLayer.matrix && adjLayer.matrix[z]) {
            const adjIdx = adjLayer.matrix[z][x];
            const adjState = palette[adjIdx] || "";
            if (adjState.toLowerCase().includes("door")) {
              adjLayer.matrix[z][x] = 0;
            }
          }
        }
      }
    }

    layer.matrix[z][x] = 0;
    this.recalculateStats();
    this.render();
    this.onUpdateCallback(this.blueprint, this.activeY);
  }

  recalculateStats() {
    if (!this.blueprint) return;
    const rawCounts = {};
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
              // Normalize state to base item ID for canonical BOM accounting
              const baseItem = state.split("[")[0].trim();
              rawCounts[baseItem] = (rawCounts[baseItem] || 0) + 1;
              totalBlocks++;
            }
            if (isSubterranean) {
              excavation++;
            }
          }
        }
      }
    }

    // BOM Accounting: Paired 2-block bed (head+foot) and 2-block door (lower+upper) count as 1 item in BOM
    const bom = {};
    for (const [baseItem, count] of Object.entries(rawCounts)) {
      const clean = baseItem.toLowerCase();
      if ((clean.includes("door") && !clean.includes("trapdoor")) || clean.includes("bed")) {
        bom[baseItem] = Math.ceil(count / 2);
      } else {
        bom[baseItem] = count;
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
    let clean = state.split("[")[0].replace("minecraft:", "").replace("#minecraft:", "").replace("#c:", "").replace("#", "");
    const parts = clean.split("_");
    if (parts.length > 2) {
      return parts.map(p => p[0].toUpperCase()).join("");
    }
    return parts.map(p => p.slice(0, 3)).join(" ");
  }

  resolveCellColor(state) {
    if (!state) return "#334155";
    let clean = state.split("[")[0].replace("minecraft:", "").replace("#minecraft:", "").replace("#c:", "").replace("#", "").toLowerCase();
    if (clean.includes("plank")) return "#7c5228";
    if (clean.includes("log") || clean.includes("wood")) return "#4a3219";
    if (clean.includes("cobble")) return "#52525b";
    if (clean.includes("stone_brick")) return "#4b5563";
    if (clean.includes("stone")) return "#64748b";
    if (clean.includes("deepslate")) return "#27272a";
    if (clean.includes("glass")) return "#0369a1";
    if (clean.includes("brick")) return "#7f1d1d";
    if (clean.includes("torch") || clean.includes("lantern") || clean.includes("light")) return "#b45309";
    if (clean.includes("barrel") || clean.includes("chest")) return "#854d0e";
    if (clean.includes("stair")) return "#7c5228";
    if (clean.includes("slab")) return "#7c5228";
    if (clean.includes("door") || clean.includes("trapdoor") || clean.includes("fence")) return "#5c3d1e";
    if (clean.includes("bed")) return "#991b1b";
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
