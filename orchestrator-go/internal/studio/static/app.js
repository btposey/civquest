// CivQuest Blueprint Studio SPA Logic
// Integrates 2D Layer Matrix Grid Editor, HTML5 Canvas Isometric 2.5D Voxel Renderer,
// and System 2 AI Architectural Assistant Chat Drawer.

// Global Studio State
window.StudioState = {
  blueprint: null,
  activeY: 0,
  isoCanvas: null,
  gridEditor: null,
};

document.addEventListener("DOMContentLoaded", () => {
  initTabs();
  initSystemHealth();
  initStudioWebSocket();
  loadBlueprints();
  loadPalette();
  loadTaxonomy();
  loadAgents();

  // Initialize Canvas & Grid Editor
  initCanvasAndGrid();
  initAssistantDrawer();
  initEditorControls();
});

// Tab Navigation
function initTabs() {
  const tabs = document.querySelectorAll(".tab-btn");
  tabs.forEach(tab => {
    tab.addEventListener("click", () => {
      tabs.forEach(t => t.classList.remove("active"));
      document.querySelectorAll(".tab-pane").forEach(p => p.classList.remove("active"));
      tab.classList.add("active");
      const targetPane = document.getElementById(tab.dataset.tab);
      if (targetPane) {
        targetPane.classList.add("active");
        // Trigger canvas re-render when switching to editor tab
        if (tab.dataset.tab === "tab-editor" && window.StudioState.isoCanvas) {
          window.StudioState.isoCanvas.render();
        }
      }
    });
  });
}

// Health Check
async function initSystemHealth() {
  const statusEl = document.getElementById("system-status");
  try {
    const res = await fetch("/api/health");
    if (res.ok) {
      const data = await res.json();
      statusEl.textContent = `Online: ${data.status}`;
      statusEl.className = "status-badge online";
    } else {
      statusEl.textContent = "System Error";
      statusEl.className = "status-badge offline";
    }
  } catch (err) {
    statusEl.textContent = "Offline";
    statusEl.className = "status-badge offline";
  }
}

// Studio WebSocket Connection
let studioWS = null;
function initStudioWebSocket() {
  const wsBadge = document.getElementById("ws-status");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const wsUrl = `${protocol}//${window.location.host}/ws/studio`;

  try {
    studioWS = new WebSocket(wsUrl);
    studioWS.onopen = () => {
      wsBadge.textContent = "Studio WS Active";
      wsBadge.className = "status-badge online";
    };
    studioWS.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        console.log("Studio WS message:", msg);
      } catch (e) {
        console.log("Studio WS raw:", event.data);
      }
    };
    studioWS.onclose = () => {
      wsBadge.textContent = "WS Disconnected";
      wsBadge.className = "status-badge offline";
      setTimeout(initStudioWebSocket, 5000);
    };
  } catch (err) {
    console.error("Failed to connect to studio WS:", err);
  }
}

// =============================================================================
// 2D Grid Editor & Isometric Canvas Initialization
// =============================================================================
function initCanvasAndGrid() {
  const canvasEl = document.getElementById("isometric-canvas");
  if (canvasEl && window.IsometricCanvas) {
    window.StudioState.isoCanvas = new window.IsometricCanvas("isometric-canvas");
  }

  const gridEl = document.getElementById("layer-grid-container");
  if (gridEl && window.LayerGridEditor) {
    window.StudioState.gridEditor = new window.LayerGridEditor("layer-grid-container", {
      onUpdate: (bp, activeY) => {
        window.StudioState.activeY = activeY;
        // Synchronize 3D isometric canvas in real time
        if (window.StudioState.isoCanvas) {
          window.StudioState.isoCanvas.setBlueprint(bp, activeY);
        }
        // Update raw JSON editor value
        const jsonInput = document.getElementById("blueprint-input");
        if (jsonInput) {
          jsonInput.value = JSON.stringify(bp, null, 2);
        }
        updateHeaderInfo(bp);
      }
    });
  }

  // Load default Pioneer Cabin sample
  loadSampleCabin();
}

function loadSampleCabin() {
  const sample = {
    id: "pioneer_cabin",
    name: "Pioneer Log Cabin",
    version: 1,
    purpose: "HOME",
    style: "pioneer",
    dimensions: { width: 5, height: 4, length: 5, min_y: 0 },
    palette: {
      1: "minecraft:oak_planks",
      2: "minecraft:oak_log[axis=y]",
      3: "minecraft:oak_stairs[facing=north]",
      4: "minecraft:glass_pane",
      5: "minecraft:torch",
      6: "minecraft:barrel"
    },
    layers: [
      {
        y: 0,
        matrix: [
          [2, 1, 1, 1, 2],
          [1, 1, 1, 1, 1],
          [1, 1, 1, 1, 1],
          [1, 1, 1, 1, 1],
          [2, 1, 1, 1, 2]
        ]
      },
      {
        y: 1,
        matrix: [
          [2, 1, 4, 1, 2],
          [1, 0, 0, 0, 1],
          [4, 0, 6, 0, 4],
          [1, 0, 0, 0, 1],
          [2, 1, 0, 1, 2]
        ]
      },
      {
        y: 2,
        matrix: [
          [2, 1, 4, 1, 2],
          [1, 0, 0, 0, 1],
          [4, 0, 5, 0, 4],
          [1, 0, 0, 0, 1],
          [2, 1, 1, 1, 2]
        ]
      },
      {
        y: 3,
        matrix: [
          [3, 3, 3, 3, 3],
          [3, 1, 1, 1, 3],
          [3, 1, 1, 1, 3],
          [3, 1, 1, 1, 3],
          [3, 3, 3, 3, 3]
        ]
      }
    ]
  };

  loadBlueprintIntoStudio(sample);
}

function loadBlueprintIntoStudio(bp) {
  window.StudioState.blueprint = bp;
  const minY = bp.dimensions && bp.dimensions.min_y !== undefined ? bp.dimensions.min_y : 0;
  window.StudioState.activeY = (0 >= minY && 0 < minY + bp.dimensions.height) ? 0 : minY;

  if (window.StudioState.gridEditor) {
    window.StudioState.gridEditor.setBlueprint(bp);
    window.StudioState.gridEditor.recalculateStats();
  }

  if (window.StudioState.isoCanvas) {
    window.StudioState.isoCanvas.setBlueprint(bp, window.StudioState.activeY);
  }

  const jsonInput = document.getElementById("blueprint-input");
  if (jsonInput) {
    jsonInput.value = JSON.stringify(bp, null, 2);
  }

  updateHeaderInfo(bp);
}

function updateHeaderInfo(bp) {
  if (!bp) return;
  const nameEl = document.getElementById("current-bp-name");
  if (nameEl) nameEl.textContent = bp.name || bp.id || "Blueprint";

  const purposeEl = document.getElementById("current-bp-purpose");
  if (purposeEl) purposeEl.textContent = bp.purpose || bp.type || "CIVIC";

  const dimsEl = document.getElementById("current-bp-dims");
  if (dimsEl && bp.dimensions) {
    dimsEl.textContent = `${bp.dimensions.width}x${bp.dimensions.height}x${bp.dimensions.length} (MinY: ${bp.dimensions.min_y || 0})`;
  }

  const contextName = document.getElementById("assistant-context-name");
  if (contextName) contextName.textContent = bp.name || bp.id || "Active Blueprint";
}

// =============================================================================
// Editor Toolbar & Orientation Controls
// =============================================================================
function initEditorControls() {
  // Elevation Stepper
  const btnUp = document.getElementById("btn-layer-up");
  const btnDown = document.getElementById("btn-layer-down");
  const btnAddBasement = document.getElementById("btn-add-basement");
  const btnAddLayer = document.getElementById("btn-add-layer");

  if (btnUp) {
    btnUp.addEventListener("click", () => {
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.stepElevation(1);
    });
  }
  if (btnDown) {
    btnDown.addEventListener("click", () => {
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.stepElevation(-1);
    });
  }
  if (btnAddBasement) {
    btnAddBasement.addEventListener("click", () => {
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.addBasementLayer();
    });
  }
  if (btnAddLayer) {
    btnAddLayer.addEventListener("click", () => {
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.addLayerAbove();
    });
  }

  // Onion-skinning toggle
  const btnOnion = document.getElementById("btn-toggle-onion");
  if (btnOnion) {
    btnOnion.addEventListener("click", () => {
      if (window.StudioState.gridEditor) {
        const active = window.StudioState.gridEditor.toggleOnionSkinning();
        btnOnion.classList.toggle("active", active);
        btnOnion.textContent = active ? "👻 Onion Skin (Y-1): ON" : "👻 Onion Skin (Y-1): OFF";
      }
    });
  }

  // X-Ray toggle
  const btnXRay = document.getElementById("btn-toggle-xray");
  if (btnXRay) {
    btnXRay.addEventListener("click", () => {
      if (window.StudioState.isoCanvas) {
        const locked = window.StudioState.isoCanvas.toggleXRay();
        btnXRay.classList.toggle("active", locked);
        btnXRay.textContent = locked ? "⚡ X-Ray Walls: LOCKED ON" : "⚡ X-Ray Walls: Auto (Hover)";
      }
    });
  }

  // Tool buttons
  const btnPlace = document.getElementById("btn-tool-place");
  const btnErase = document.getElementById("btn-tool-erase");
  if (btnPlace && btnErase) {
    btnPlace.addEventListener("click", () => {
      btnPlace.classList.add("active-tool");
      btnErase.classList.remove("active-tool");
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.activeTool = "place";
    });
    btnErase.addEventListener("click", () => {
      btnErase.classList.add("active-tool");
      btnPlace.classList.remove("active-tool");
      if (window.StudioState.gridEditor) window.StudioState.gridEditor.activeTool = "erase";
    });
  }

  // Quick Palette Chips
  const chips = document.querySelectorAll(".quick-palette-chips .palette-chip");
  chips.forEach(chip => {
    chip.addEventListener("click", () => {
      chips.forEach(c => c.classList.remove("active"));
      chip.classList.add("active");
      const blockId = chip.dataset.id;
      if (blockId === "air") {
        if (window.StudioState.gridEditor) window.StudioState.gridEditor.activeTool = "erase";
        if (btnErase) btnErase.click();
      } else {
        if (window.StudioState.gridEditor) {
          window.StudioState.gridEditor.activeTool = "place";
          window.StudioState.gridEditor.selectedBlockId = blockId;
        }
        if (btnPlace) btnPlace.click();
        const sel = document.getElementById("palette-block-select");
        if (sel) sel.value = blockId;
      }
    });
  });

  // Orientation Controls
  const selFacing = document.getElementById("orient-facing");
  const selAxis = document.getElementById("orient-axis");
  const selHalf = document.getElementById("orient-half");
  const btnRotate = document.getElementById("btn-rotate-block");

  if (selFacing) {
    selFacing.addEventListener("change", (e) => {
      if (window.StudioState.gridEditor) {
        window.StudioState.gridEditor.orientation.facing = e.target.value;
        window.StudioState.gridEditor.showRotationFlash();
      }
    });
  }

  if (selAxis) {
    selAxis.addEventListener("change", (e) => {
      if (window.StudioState.gridEditor) {
        window.StudioState.gridEditor.orientation.axis = e.target.value;
        window.StudioState.gridEditor.showRotationFlash();
      }
    });
  }

  if (selHalf) {
    selHalf.addEventListener("change", (e) => {
      if (window.StudioState.gridEditor) {
        window.StudioState.gridEditor.orientation.half = e.target.value;
      }
    });
  }

  if (btnRotate) {
    btnRotate.addEventListener("click", () => {
      if (window.StudioState.gridEditor) {
        window.StudioState.gridEditor.rotateClockwise90();
      }
    });
  }

  // Listen for orientation change events (e.g. triggered via 'R' hotkey)
  window.addEventListener("orientation-changed", (e) => {
    const o = e.detail;
    if (selFacing && o.facing) selFacing.value = o.facing;
    if (selAxis && o.axis) selAxis.value = o.axis;
    if (selHalf && o.half) selHalf.value = o.half;
  });

  // Action Buttons
  const btnLoadSample = document.getElementById("btn-load-sample");
  if (btnLoadSample) btnLoadSample.addEventListener("click", loadSampleCabin);

  const btnValidate = document.getElementById("btn-validate");
  if (btnValidate) btnValidate.addEventListener("click", validateCurrentBlueprint);

  const btnSave = document.getElementById("btn-save-blueprint");
  if (btnSave) {
    btnSave.addEventListener("click", async () => {
      const bp = window.StudioState.blueprint;
      if (!bp) return;
      try {
        const res = await fetch("/api/blueprints", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(bp)
        });
        if (!res.ok) {
          const err = await res.json();
          throw new Error(err.error || `HTTP ${res.status}`);
        }
        alert(`Blueprint "${bp.name}" successfully saved to primary library!`);
        loadBlueprints();
      } catch (e) {
        alert(`Save failed: ${e.message}`);
      }
    });
  }

  // Toggle Raw JSON Panel
  const btnToggleJson = document.getElementById("btn-toggle-json");
  const rawJsonPanel = document.getElementById("raw-json-panel");
  const btnCloseJson = document.getElementById("btn-close-json-panel");

  if (btnToggleJson && rawJsonPanel) {
    btnToggleJson.addEventListener("click", () => {
      rawJsonPanel.classList.toggle("hidden");
      if (!rawJsonPanel.classList.contains("hidden")) {
        validateCurrentBlueprint();
      }
    });
  }

  if (btnCloseJson && rawJsonPanel) {
    btnCloseJson.addEventListener("click", () => {
      rawJsonPanel.classList.add("hidden");
    });
  }

  const jsonInput = document.getElementById("blueprint-input");
  if (jsonInput) {
    jsonInput.addEventListener("input", () => {
      try {
        const parsed = JSON.parse(jsonInput.value);
        window.StudioState.blueprint = parsed;
        if (window.StudioState.gridEditor) window.StudioState.gridEditor.setBlueprint(parsed);
        if (window.StudioState.isoCanvas) window.StudioState.isoCanvas.setBlueprint(parsed);
        updateHeaderInfo(parsed);
      } catch (e) {
        // Wait for valid JSON
      }
    });
  }

  const btnRefreshBp = document.getElementById("btn-refresh-blueprints");
  if (btnRefreshBp) btnRefreshBp.addEventListener("click", loadBlueprints);
}

// =============================================================================
// AI Architectural Assistant Chat Drawer (cq-imw.7)
// =============================================================================
function initAssistantDrawer() {
  const drawer = document.getElementById("ai-assistant-drawer");
  const btnOpen = document.getElementById("btn-open-assistant");
  const btnClose = document.getElementById("btn-close-assistant");
  const btnSend = document.getElementById("btn-send-assistant");
  const promptInput = document.getElementById("assistant-prompt-input");
  const styleSelect = document.getElementById("assistant-style-select");
  const loadingIndicator = document.getElementById("assistant-loading");
  const historyStream = document.getElementById("assistant-chat-history");

  if (btnOpen && drawer) {
    btnOpen.addEventListener("click", () => drawer.classList.remove("hidden"));
  }

  if (btnClose && drawer) {
    btnClose.addEventListener("click", () => drawer.classList.add("hidden"));
  }

  // Suggestion chips click-to-populate
  document.querySelectorAll(".chip-suggestion").forEach(chip => {
    chip.addEventListener("click", () => {
      if (promptInput) promptInput.value = chip.dataset.prompt;
      if (styleSelect && chip.dataset.style) styleSelect.value = chip.dataset.style;
      if (promptInput) promptInput.focus();
    });
  });

  if (btnSend && promptInput) {
    btnSend.addEventListener("click", async () => {
      const text = promptInput.value.trim();
      if (!text) return;

      const style = styleSelect ? styleSelect.value : "frontier";
      const baseBpId = window.StudioState.blueprint ? window.StudioState.blueprint.id : "";

      // 1. Append User Message Bubble
      appendChatMessage("user", "Operator", text);
      promptInput.value = "";

      // 2. Show thinking indicator
      if (loadingIndicator) loadingIndicator.classList.remove("hidden");
      btnSend.disabled = true;

      try {
        const res = await fetch("/api/assistant/draft", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            prompt: text,
            style: style,
            base_blueprint_id: baseBpId
          })
        });

        if (!res.ok) {
          const err = await res.json();
          throw new Error(err.error || `HTTP ${res.status}`);
        }

        const draftedBP = await res.json();

        // 3. Immediately Load Blueprint into 2D Grid & 3D Isometric Canvas!
        loadBlueprintIntoStudio(draftedBP);

        // 4. Switch to Editor Tab if not already active
        const editorTab = document.querySelector('[data-tab="tab-editor"]');
        if (editorTab && !editorTab.classList.contains("active")) {
          editorTab.click();
        }

        const totalBlocks = draftedBP.bom ? Object.values(draftedBP.bom).reduce((a, b) => a + b, 0) : 0;
        const responseSummary = `
          ✨ <strong>Successfully synthesized blueprint:</strong> "${escapeHTML(draftedBP.name)}"<br>
          &bull; <strong>Dimensions:</strong> ${draftedBP.dimensions.width}x${draftedBP.dimensions.height}x${draftedBP.dimensions.length} (Min Y: ${draftedBP.dimensions.min_y || 0})<br>
          &bull; <strong>Purpose:</strong> ${escapeHTML(draftedBP.purpose)} &bull; <strong>Style:</strong> ${escapeHTML(draftedBP.style || style)}<br>
          &bull; <strong>Excavation Volume:</strong> ${draftedBP.excavation || 0} blocks<br>
          &bull; <strong>Total Voxels:</strong> ${totalBlocks} blocks<br><br>
          <em>Blueprint loaded directly into the 2D layer matrix editor and 3D isometric canvas! You can continue chatting to refine or modify.</em>
        `;

        appendChatMessage("architect", "🏛 Master Architect", responseSummary, true);
      } catch (err) {
        appendChatMessage("architect", "🏛 Master Architect", `⚠️ Synthesis error: ${escapeHTML(err.message)}`);
      } finally {
        if (loadingIndicator) loadingIndicator.classList.add("hidden");
        btnSend.disabled = false;
      }
    });
  }
}

function appendChatMessage(role, meta, content, isHTML = false) {
  const history = document.getElementById("assistant-chat-history");
  if (!history) return;

  const msgDiv = document.createElement("div");
  msgDiv.className = `chat-message message-${role}`;

  const metaDiv = document.createElement("div");
  metaDiv.className = "message-meta";
  metaDiv.textContent = meta;

  const bodyDiv = document.createElement("div");
  bodyDiv.className = "message-body";
  if (isHTML) {
    bodyDiv.innerHTML = content;
  } else {
    bodyDiv.textContent = content;
  }

  msgDiv.appendChild(metaDiv);
  msgDiv.appendChild(bodyDiv);
  history.appendChild(msgDiv);
  history.scrollTop = history.scrollHeight;
}

// =============================================================================
// Blueprints API
// =============================================================================
async function loadBlueprints() {
  const grid = document.getElementById("blueprints-grid");
  if (!grid) return;
  grid.innerHTML = '<p class="placeholder-text">Loading blueprints...</p>';

  try {
    const res = await fetch("/api/blueprints");
    const data = await res.json();
    const blueprints = Array.isArray(data) ? data : (data.blueprints || []);

    if (blueprints.length === 0) {
      grid.innerHTML = '<p class="placeholder-text">No blueprints found in library. Use the Editor tab to create one.</p>';
      return;
    }

    grid.innerHTML = "";
    blueprints.forEach(bp => {
      const card = document.createElement("div");
      card.className = "card";
      const totalBlocks = bp.bom ? Object.values(bp.bom).reduce((a, b) => a + b, 0) : 0;
      card.innerHTML = `
        <div class="card-title">
          <span>${escapeHTML(bp.name || bp.id)}</span>
          <span class="card-badge">${escapeHTML(bp.purpose || bp.type || "CIVIC")}</span>
        </div>
        <div class="card-desc">${escapeHTML(bp.description || "Architectural construction plan")}</div>
        <div class="card-meta">
          <strong>ID:</strong> ${escapeHTML(bp.id)} &bull;
          <strong>Size:</strong> ${bp.dimensions ? `${bp.dimensions.width}x${bp.dimensions.height}x${bp.dimensions.length}` : "N/A"} &bull;
          <strong>Blocks:</strong> ${totalBlocks} &bull;
          <strong>Min Y:</strong> ${bp.dimensions ? (bp.dimensions.min_y || 0) : 0}
        </div>
        <div class="card-actions">
          <button class="btn btn-sm btn-view-bp" data-id="${escapeHTML(bp.id)}">Inspect</button>
          <button class="btn btn-sm btn-edit-bp" data-id="${escapeHTML(bp.id)}">Edit in Studio</button>
          <button class="btn btn-sm btn-danger btn-delete-bp" data-id="${escapeHTML(bp.id)}">Delete</button>
        </div>
      `;
      grid.appendChild(card);
    });

    document.querySelectorAll(".btn-view-bp").forEach(btn => {
      btn.addEventListener("click", () => inspectBlueprint(btn.dataset.id));
    });
    document.querySelectorAll(".btn-edit-bp").forEach(btn => {
      btn.addEventListener("click", () => editBlueprintInEditor(btn.dataset.id));
    });
    document.querySelectorAll(".btn-delete-bp").forEach(btn => {
      btn.addEventListener("click", () => deleteBlueprint(btn.dataset.id));
    });
  } catch (err) {
    grid.innerHTML = `<p class="placeholder-text" style="color:var(--danger)">Error loading blueprints: ${err.message}</p>`;
  }
}

async function inspectBlueprint(id) {
  try {
    const res = await fetch(`/api/blueprints/${encodeURIComponent(id)}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const bp = await res.json();
    alert(`Blueprint ${bp.name || bp.id}\nPurpose: ${bp.purpose}\nExcavation Volume: ${bp.excavation || 0} blocks\nTotal BOM items: ${Object.keys(bp.bom || {}).length}`);
  } catch (e) {
    alert(`Failed to load blueprint: ${e.message}`);
  }
}

async function editBlueprintInEditor(id) {
  try {
    const res = await fetch(`/api/blueprints/${encodeURIComponent(id)}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const bp = await res.json();
    loadBlueprintIntoStudio(bp);
    // Switch to editor tab
    document.querySelector('[data-tab="tab-editor"]').click();
  } catch (e) {
    alert(`Failed to load blueprint into editor: ${e.message}`);
  }
}

async function deleteBlueprint(id) {
  if (!confirm(`Are you sure you want to delete blueprint "${id}"?`)) return;
  try {
    const res = await fetch(`/api/blueprints/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    loadBlueprints();
  } catch (e) {
    alert(`Failed to delete blueprint: ${e.message}`);
  }
}

async function validateCurrentBlueprint() {
  const output = document.getElementById("validation-output");
  if (!output) return;
  const raw = document.getElementById("blueprint-input").value;
  output.innerHTML = '<p class="placeholder-text">Validating blueprint specification...</p>';

  try {
    const res = await fetch("/api/blueprints/validate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: raw
    });
    const result = await res.json();
    if (!res.ok || result.valid === false) {
      const errors = result.errors || [result.error || "Validation failed"];
      output.innerHTML = `
        <div style="color:var(--danger)">
          <h4>Validation Errors:</h4>
          <ul>${errors.map(e => `<li>${escapeHTML(e)}</li>`).join("")}</ul>
        </div>
      `;
      return;
    }

    const bomList = Object.entries(result.bom || {})
      .map(([k, v]) => `<li><code>${escapeHTML(k)}</code>: <strong>${v}</strong></li>`)
      .join("");

    output.innerHTML = `
      <div style="color:var(--success)">
        <h4>&check; Blueprint Valid</h4>
      </div>
      <div style="margin-top:0.75rem">
        <p><strong>Dimensions:</strong> ${result.dimensions.width}x${result.dimensions.height}x${result.dimensions.length} (Min Y: ${result.dimensions.min_y || 0})</p>
        <p><strong>Excavation Volume:</strong> ${result.excavation || 0} blocks</p>
        <h5 style="margin-top:0.75rem">Computed Bill of Materials:</h5>
        <ul style="margin-left:1.25rem; margin-top:0.25rem">${bomList || "<li>No blocks placed</li>"}</ul>
      </div>
    `;
  } catch (err) {
    output.innerHTML = `<p style="color:var(--danger)">Validation request failed: ${escapeHTML(err.message)}</p>`;
  }
}

// =============================================================================
// Palette API & Catalog Select
// =============================================================================
async function loadPalette() {
  const tbody = document.getElementById("palette-tbody");
  const blockSelect = document.getElementById("palette-block-select");

  try {
    const res = await fetch("/api/palette");
    const data = await res.json();
    const blocks = Array.isArray(data) ? data : (data.blocks || []);

    window._paletteCatalog = blocks;
    renderPaletteTable(blocks);

    if (blockSelect) {
      blockSelect.innerHTML = "";
      blocks.forEach(b => {
        const opt = document.createElement("option");
        opt.value = b.id;
        opt.textContent = `${b.id.replace("minecraft:", "")} (${b.category || "none"})`;
        blockSelect.appendChild(opt);
      });

      blockSelect.addEventListener("change", (e) => {
        if (window.StudioState.gridEditor) {
          window.StudioState.gridEditor.selectedBlockId = e.target.value;
        }
      });
    }

    const searchInput = document.getElementById("palette-search");
    if (searchInput) {
      searchInput.addEventListener("input", (e) => {
        const q = e.target.value.toLowerCase();
        const filtered = (window._paletteCatalog || []).filter(b => b.id.toLowerCase().includes(q));
        renderPaletteTable(filtered);
      });
    }
  } catch (err) {
    if (tbody) tbody.innerHTML = `<tr><td colspan="4" style="color:var(--danger)">Error: ${err.message}</td></tr>`;
  }
}

function renderPaletteTable(blocks) {
  const tbody = document.getElementById("palette-tbody");
  if (!tbody) return;
  tbody.innerHTML = "";
  if (blocks.length === 0) {
    tbody.innerHTML = '<tr><td colspan="4" class="placeholder-text">No matching blocks found</td></tr>';
    return;
  }
  blocks.forEach(b => {
    const tr = document.createElement("tr");
    const props = b.properties || b.default_properties ? JSON.stringify(b.properties || b.default_properties) : "-";
    const glyph = b.glyph || b.direction_glyph || "•";
    const cat = b.category || b.orientation_category || "none";
    tr.innerHTML = `
      <td><code>${escapeHTML(b.id)}</code></td>
      <td><span class="card-badge">${escapeHTML(cat)}</span></td>
      <td><code>${escapeHTML(props)}</code></td>
      <td><span class="glyph-badge">${escapeHTML(glyph)}</span></td>
    `;
    tbody.appendChild(tr);
  });
}

// =============================================================================
// Taxonomy API
// =============================================================================
async function loadTaxonomy() {
  const grid = document.getElementById("taxonomy-grid");
  if (!grid) return;
  try {
    const res = await fetch("/api/taxonomy");
    const data = await res.json();
    const map = data.taxonomy || data.map || {};
    grid.innerHTML = "";

    for (const [tag, items] of Object.entries(map)) {
      const card = document.createElement("div");
      card.className = "card";
      card.innerHTML = `
        <div class="card-title">
          <span>${escapeHTML(tag)}</span>
          <span class="card-badge">${items.length} items</span>
        </div>
        <div class="card-desc">Substitutable in-situ material tag</div>
        <ul style="font-size:0.8rem; color:#9ca3af; margin-left:1rem; max-height:120px; overflow-y:auto">
          ${items.map(i => `<li><code>${escapeHTML(i)}</code></li>`).join("")}
        </ul>
      `;
      grid.appendChild(card);
    }
  } catch (err) {
    grid.innerHTML = `<p style="color:var(--danger)">Error loading taxonomy: ${err.message}</p>`;
  }
}

// =============================================================================
// Agents & Sockets API
// =============================================================================
let activeAgentWS = null;
async function loadAgents() {
  const grid = document.getElementById("agents-grid");
  if (!grid) return;
  grid.innerHTML = '<p class="placeholder-text">Scanning agent sockets & manifests...</p>';

  try {
    const res = await fetch("/api/agents");
    const data = await res.json();
    const agents = Array.isArray(data) ? data : (data.agents || []);

    if (agents.length === 0) {
      grid.innerHTML = '<p class="placeholder-text">No agents found in socket or manifest directories.</p>';
      return;
    }

    grid.innerHTML = "";
    agents.forEach(a => {
      const card = document.createElement("div");
      card.className = "card";
      const statusClass = a.active ? "online" : "offline";
      const statusText = a.active ? "RUNNING" : "STOPPED";
      card.innerHTML = `
        <div class="card-title">
          <span>${escapeHTML(a.name || a.id)}</span>
          <span class="status-badge ${statusClass}">${statusText}</span>
        </div>
        <div class="card-meta">
          <strong>Clan:</strong> ${escapeHTML(a.clan_id || "None")} &bull;
          <strong>Profession:</strong> ${escapeHTML(a.profession || "Pioneer")}
        </div>
        <div class="card-desc">Socket: <code>${escapeHTML(a.socket_path || "N/A")}</code></div>
        <div class="card-actions">
          <button class="btn btn-sm btn-status-agent" data-id="${escapeHTML(a.id || a.name)}">Fetch Status</button>
          <button class="btn btn-sm btn-primary btn-stream-agent" data-id="${escapeHTML(a.id || a.name)}">Live Stream (WS)</button>
        </div>
      `;
      grid.appendChild(card);
    });

    document.querySelectorAll(".btn-status-agent").forEach(btn => {
      btn.addEventListener("click", () => fetchAgentStatus(btn.dataset.id));
    });
    document.querySelectorAll(".btn-stream-agent").forEach(btn => {
      btn.addEventListener("click", () => startAgentStream(btn.dataset.id));
    });
  } catch (err) {
    grid.innerHTML = `<p style="color:var(--danger)">Error listing agents: ${err.message}</p>`;
  }

  const btnRefresh = document.getElementById("btn-refresh-agents");
  if (btnRefresh) btnRefresh.addEventListener("click", loadAgents);

  const btnClose = document.getElementById("btn-close-telemetry");
  if (btnClose) {
    btnClose.addEventListener("click", () => {
      document.getElementById("agent-telemetry-panel").classList.add("hidden");
      if (activeAgentWS) {
        activeAgentWS.close();
        activeAgentWS = null;
      }
    });
  }
}

async function fetchAgentStatus(id) {
  const panel = document.getElementById("agent-telemetry-panel");
  const nameEl = document.getElementById("active-agent-name");
  const dataEl = document.getElementById("agent-telemetry-data");

  panel.classList.remove("hidden");
  nameEl.textContent = `Agent Snapshot: ${id}`;
  dataEl.textContent = "Querying agent socket...";

  try {
    const res = await fetch(`/api/agents/${encodeURIComponent(id)}/status`);
    const data = await res.json();
    dataEl.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    dataEl.textContent = `Failed to query agent socket: ${err.message}`;
  }
}

function startAgentStream(id) {
  if (activeAgentWS) {
    activeAgentWS.close();
    activeAgentWS = null;
  }

  const panel = document.getElementById("agent-telemetry-panel");
  const nameEl = document.getElementById("active-agent-name");
  const dataEl = document.getElementById("agent-telemetry-data");

  panel.classList.remove("hidden");
  nameEl.textContent = `Live Telemetry (WS): ${id}`;
  dataEl.textContent = "Connecting to WebSocket stream...";

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const wsUrl = `${protocol}//${window.location.host}/ws/agents/${encodeURIComponent(id)}`;

  try {
    activeAgentWS = new WebSocket(wsUrl);
    activeAgentWS.onmessage = (event) => {
      try {
        const parsed = JSON.parse(event.data);
        dataEl.textContent = JSON.stringify(parsed, null, 2);
      } catch (e) {
        dataEl.textContent = event.data;
      }
    };
    activeAgentWS.onerror = (e) => {
      dataEl.textContent = `WebSocket Error: ${JSON.stringify(e)}`;
    };
    activeAgentWS.onclose = () => {
      dataEl.textContent += "\n[Stream closed by server or client]";
    };
  } catch (err) {
    dataEl.textContent = `WebSocket connection failed: ${err.message}`;
  }
}

function escapeHTML(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}
