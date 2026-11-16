// CivQuest Blueprint Studio SPA Logic

document.addEventListener("DOMContentLoaded", () => {
  initTabs();
  initSystemHealth();
  initStudioWebSocket();
  loadBlueprints();
  loadPalette();
  loadTaxonomy();
  loadAgents();
  initEditor();
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
      if (targetPane) targetPane.classList.add("active");
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

// Blueprints API
async function loadBlueprints() {
  const grid = document.getElementById("blueprints-grid");
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
          <strong>Blocks:</strong> ${totalBlocks}
        </div>
        <div class="card-actions">
          <button class="btn btn-sm btn-view-bp" data-id="${escapeHTML(bp.id)}">Inspect</button>
          <button class="btn btn-sm btn-edit-bp" data-id="${escapeHTML(bp.id)}">Edit</button>
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
    document.getElementById("blueprint-input").value = JSON.stringify(bp, null, 2);
    // Switch to editor tab
    document.querySelector('[data-tab="tab-editor"]').click();
    validateCurrentBlueprint();
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

// Editor & Validator
function initEditor() {
  document.getElementById("btn-load-sample").addEventListener("click", () => {
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
        4: "minecraft:glass_pane"
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
            [4, 0, 0, 0, 4],
            [1, 0, 0, 0, 1],
            [2, 1, 1, 1, 2]
          ]
        }
      ]
    };
    document.getElementById("blueprint-input").value = JSON.stringify(sample, null, 2);
    validateCurrentBlueprint();
  });

  document.getElementById("btn-validate").addEventListener("click", validateCurrentBlueprint);

  document.getElementById("btn-save-blueprint").addEventListener("click", async () => {
    const raw = document.getElementById("blueprint-input").value;
    try {
      const bp = JSON.parse(raw);
      const res = await fetch("/api/blueprints", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(bp)
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || `HTTP ${res.status}`);
      }
      alert("Blueprint saved successfully to primary library!");
      loadBlueprints();
    } catch (e) {
      alert(`Save failed: ${e.message}`);
    }
  });

  document.getElementById("btn-refresh-blueprints").addEventListener("click", loadBlueprints);
}

async function validateCurrentBlueprint() {
  const output = document.getElementById("validation-output");
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

// Palette API
async function loadPalette() {
  const tbody = document.getElementById("palette-tbody");
  try {
    const res = await fetch("/api/palette");
    const data = await res.json();
    const blocks = Array.isArray(data) ? data : (data.blocks || []);

    window._paletteCatalog = blocks;
    renderPaletteTable(blocks);

    document.getElementById("palette-search").addEventListener("input", (e) => {
      const q = e.target.value.toLowerCase();
      const filtered = (window._paletteCatalog || []).filter(b => b.id.toLowerCase().includes(q));
      renderPaletteTable(filtered);
    });
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="4" style="color:var(--danger)">Error: ${err.message}</td></tr>`;
  }
}

function renderPaletteTable(blocks) {
  const tbody = document.getElementById("palette-tbody");
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

// Taxonomy API
async function loadTaxonomy() {
  const grid = document.getElementById("taxonomy-grid");
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

// Agents & Sockets API + WebSockets
let activeAgentWS = null;
async function loadAgents() {
  const grid = document.getElementById("agents-grid");
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

  document.getElementById("btn-refresh-agents").addEventListener("click", loadAgents);
  document.getElementById("btn-close-telemetry").addEventListener("click", () => {
    document.getElementById("agent-telemetry-panel").classList.add("hidden");
    if (activeAgentWS) {
      activeAgentWS.close();
      activeAgentWS = null;
    }
  });
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
