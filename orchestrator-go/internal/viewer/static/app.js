// CivQuest // Tactical Agent Viewer SPA

(function() {
  'use strict';

  // State
  let currentAgentId = null;
  let ws = null;
  let reconnectTimer = null;
  let pollInterval = null;
  let cachedAgents = [];
  let latestTelemetry = null;

  // DOM Elements
  const agentSelect = document.getElementById('agent-select');
  const btnRefresh = document.getElementById('btn-refresh');
  const wsPulse = document.getElementById('ws-pulse');
  const statusBadge = document.getElementById('status-badge');

  // Header Elements
  const hdrAgentId = document.getElementById('hdr-agent-id');
  const hdrClan = document.getElementById('hdr-clan');
  const hdrProfession = document.getElementById('hdr-profession');
  const hdrHobby = document.getElementById('hdr-hobby');
  const hdrPid = document.getElementById('hdr-pid');
  const hdrUptime = document.getElementById('hdr-uptime');
  const hdrRss = document.getElementById('hdr-rss');
  const hdrLatency = document.getElementById('hdr-latency');

  // HTPDA Elements
  const breadcrumbsContainer = document.getElementById('breadcrumbs-container');
  const stackDepthBadge = document.getElementById('stack-depth-badge');
  const transitionReason = document.getElementById('transition-reason');

  // Circadian Elements
  const circadianShiftBadge = document.getElementById('circadian-shift-badge');
  const mcDigitalTime = document.getElementById('mc-digital-time');
  const mcDayCount = document.getElementById('mc-day-count');
  const mcTicks = document.getElementById('mc-ticks');
  const shiftCursor = document.getElementById('shift-cursor');
  const circadianWeather = document.getElementById('circadian-weather');
  const circadianTavern = document.getElementById('circadian-tavern');
  const circadianDusk = document.getElementById('circadian-dusk');

  // Vitals Elements
  const armorRatingBadge = document.getElementById('armor-rating-badge');
  const healthVal = document.getElementById('health-val');
  const healthBar = document.getElementById('health-bar');
  const foodVal = document.getElementById('food-val');
  const foodBar = document.getElementById('food-bar');
  const staminaVal = document.getElementById('stamina-val');
  const staminaBar = document.getElementById('stamina-bar');
  const posCoords = document.getElementById('pos-coords');
  const posFacing = document.getElementById('pos-facing');
  const envBiome = document.getElementById('env-biome');
  const navState = document.getElementById('nav-state');

  // Utility Meters
  const uHunger = document.getElementById('u-hunger');
  const uHungerVal = document.getElementById('u-hunger-val');
  const uThreat = document.getElementById('u-threat');
  const uThreatVal = document.getElementById('u-threat-val');
  const uConfidence = document.getElementById('u-confidence');
  const uConfidenceVal = document.getElementById('u-confidence-val');
  const uExplore = document.getElementById('u-explore');
  const uExploreVal = document.getElementById('u-explore-val');
  const uSleep = document.getElementById('u-sleep');
  const uSleepVal = document.getElementById('u-sleep-val');

  // Inventory Elements
  const invCountBadge = document.getElementById('inv-count-badge');
  const mainStorageGrid = document.getElementById('main-storage-grid');
  const hotbarGrid = document.getElementById('hotbar-grid');
  const itemTooltip = document.getElementById('item-tooltip');
  const ttName = document.getElementById('tt-name');
  const ttId = document.getElementById('tt-id');
  const ttCount = document.getElementById('tt-count');
  const ttDurability = document.getElementById('tt-durability');

  // FactGraph Elements
  const blackboardSearch = document.getElementById('blackboard-search');
  const factsCountBadge = document.getElementById('facts-count-badge');
  const factsList = document.getElementById('facts-list');

  // System 2 Elements
  const llmRequestsBadge = document.getElementById('llm-requests-badge');
  const goalImmediate = document.getElementById('goal-immediate');
  const goalShort = document.getElementById('goal-short');
  const goalLong = document.getElementById('goal-long');
  const reflectionContemplation = document.getElementById('reflection-contemplation');
  const reflectionDialogue = document.getElementById('reflection-dialogue');
  const reflectionChronicle = document.getElementById('reflection-chronicle');

  // Build Static Inventory Slots DOM
  function initInventoryGrid() {
    // 27 Main Storage Slots (9 - 35)
    mainStorageGrid.innerHTML = '';
    for (let slot = 9; slot <= 35; slot++) {
      const slotEl = createSlotElement(slot);
      mainStorageGrid.appendChild(slotEl);
    }

    // 9 Hotbar Slots (0 - 8)
    hotbarGrid.innerHTML = '';
    for (let slot = 0; slot <= 8; slot++) {
      const slotEl = createSlotElement(slot);
      if (slot === 0) slotEl.classList.add('main-hand');
      hotbarGrid.appendChild(slotEl);
    }
  }

  function createSlotElement(slotIndex) {
    const el = document.createElement('div');
    el.className = 'inv-slot';
    el.id = `slot-${slotIndex}`;
    el.dataset.slot = slotIndex;
    return el;
  }

  // Tooltip Event Handlers
  function attachSlotTooltip(slotEl, item) {
    slotEl.onmouseenter = (e) => {
      ttName.textContent = item.custom_name || formatItemName(item.item_id);
      ttId.textContent = item.item_id;
      ttCount.textContent = `Count: ${item.count}`;
      if (item.max_durability && item.max_durability > 0) {
        ttDurability.textContent = `Durability: ${item.durability} / ${item.max_durability}`;
        ttDurability.style.display = 'block';
      } else {
        ttDurability.style.display = 'none';
      }

      itemTooltip.classList.remove('hidden');
      updateTooltipPos(e);
    };

    slotEl.onmousemove = updateTooltipPos;
    slotEl.onmouseleave = () => {
      itemTooltip.classList.add('hidden');
    };
  }

  function updateTooltipPos(e) {
    itemTooltip.style.left = (e.clientX + 12) + 'px';
    itemTooltip.style.top = (e.clientY + 12) + 'px';
  }

  function formatItemName(rawID) {
    if (!rawID) return '';
    const name = rawID.includes(':') ? rawID.split(':')[1] : rawID;
    return name.split('_').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
  }

  // Fetch Agents List
  async function fetchAgents() {
    try {
      const res = await fetch('/api/agents');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const agents = await res.json();
      cachedAgents = agents || [];
      populateAgentSelector(cachedAgents);
    } catch (err) {
      console.error('Failed fetching agents:', err);
    }
  }

  function populateAgentSelector(agents) {
    const previous = currentAgentId;
    agentSelect.innerHTML = '';

    if (!agents || agents.length === 0) {
      const opt = document.createElement('option');
      opt.value = '';
      opt.textContent = 'No agents found';
      agentSelect.appendChild(opt);
      return;
    }

    let defaultSelect = null;
    const urlParams = new URLSearchParams(window.location.search);
    const requestedAgent = urlParams.get('agent');

    agents.forEach(a => {
      const opt = document.createElement('option');
      opt.value = a.id;
      const statusIcon = a.active ? '🟢' : '⚪';
      opt.textContent = `${statusIcon} ${a.name || a.id} (${a.profession || 'Agent'})`;
      agentSelect.appendChild(opt);

      if (requestedAgent && (a.id === requestedAgent || a.name === requestedAgent)) {
        defaultSelect = a.id;
      }
    });

    if (defaultSelect) {
      agentSelect.value = defaultSelect;
    } else if (previous && agents.some(a => a.id === previous)) {
      agentSelect.value = previous;
    } else if (agents.length > 0) {
      // Pick first active agent if available, else first agent
      const firstActive = agents.find(a => a.active);
      agentSelect.value = firstActive ? firstActive.id : agents[0].id;
    }

    if (agentSelect.value && agentSelect.value !== currentAgentId) {
      selectAgent(agentSelect.value);
    }
  }

  function selectAgent(agentID) {
    if (!agentID) return;
    currentAgentId = agentID;

    // Update URL query parameter without page reload
    const url = new URL(window.location);
    url.searchParams.set('agent', agentID);
    window.history.replaceState({}, '', url);

    connectWebSocket(agentID);
  }

  // WebSocket Telemetry Connection
  function connectWebSocket(agentID) {
    if (ws) {
      ws.onclose = null;
      ws.close();
      ws = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${window.location.host}/ws/agents/${encodeURIComponent(agentID)}`;

    try {
      ws = new WebSocket(wsUrl);
    } catch (e) {
      console.error('WebSocket creation error:', e);
      startPolling(agentID);
      return;
    }

    ws.onopen = () => {
      wsPulse.className = 'pulse-indicator online';
      statusBadge.className = 'status-badge online';
      statusBadge.textContent = 'STREAMING';
    };

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        renderTelemetry(data);
      } catch (err) {
        console.error('Failed parsing telemetry frame:', err);
      }
    };

    ws.onerror = (err) => {
      console.warn('WebSocket error, falling back to polling:', err);
    };

    ws.onclose = () => {
      wsPulse.className = 'pulse-indicator offline';
      statusBadge.className = 'status-badge offline';
      statusBadge.textContent = 'OFFLINE';

      // Fallback: poll agent status REST endpoint
      startPolling(agentID);

      // Attempt WS reconnect in 2 seconds
      reconnectTimer = setTimeout(() => {
        if (currentAgentId === agentID) {
          connectWebSocket(agentID);
        }
      }, 2000);
    };
  }

  function startPolling(agentID) {
    if (pollInterval) clearInterval(pollInterval);
    pollInterval = setInterval(async () => {
      if (currentAgentId !== agentID) {
        clearInterval(pollInterval);
        return;
      }
      try {
        const res = await fetch(`/api/agents/${encodeURIComponent(agentID)}/status`);
        if (res.ok) {
          const data = await res.json();
          renderTelemetry(data);
        }
      } catch (e) {
        // silent poll failure
      }
    }, 1000);
  }

  // Render Telemetry Snapshot
  function renderTelemetry(data) {
    latestTelemetry = data;

    const isActive = data.active !== false && data.status !== 'OFFLINE';

    // 1. Connection & Header Bar
    if (isActive) {
      wsPulse.className = 'pulse-indicator online';
      statusBadge.className = 'status-badge online';
      statusBadge.textContent = data.status || 'ACTIVE';
    } else {
      wsPulse.className = 'pulse-indicator offline';
      statusBadge.className = 'status-badge offline';
      statusBadge.textContent = 'OFFLINE';
    }

    hdrAgentId.textContent = data.agent || currentAgentId || '--';
    hdrClan.textContent = data.clan || '--';

    // Match cached agent info for profession/hobby if missing in frame
    const agentInfo = cachedAgents.find(a => a.id === (data.agent || currentAgentId));
    hdrProfession.textContent = data.profession || (agentInfo ? agentInfo.profession : '--');
    hdrHobby.textContent = data.secondary_hobby || (agentInfo ? agentInfo.secondary_hobby : '--');

    hdrPid.textContent = data.pid && data.pid > 0 ? data.pid : '--';
    hdrUptime.textContent = data.uptime_seconds ? `${data.uptime_seconds.toFixed(1)}s` : '--';
    hdrRss.textContent = data.memory_rss_mb ? `${data.memory_rss_mb} MB` : '--';
    hdrLatency.textContent = data.frame_latency_ms ? `${data.frame_latency_ms.toFixed(2)} ms` : '--';

    // 2. Panel 1: HTPDA State Stack Breadcrumbs
    renderHTPDABreadcrumbs(data);

    // 3. Panel 2: Circadian Shift Clock
    renderCircadianClock(data);

    // 4. Panel 3: Settler Vitals & Pose
    renderVitals(data);

    // 5. Panel 4: Equipment & Inventory Matrix
    renderInventory(data);

    // 6. Panel 5: FactGraph Blackboard
    renderBlackboard(data.blackboard);

    // 7. Panel 6: System 2 Reflections
    renderReflections(data);
  }

  // Render HTPDA Breadcrumbs
  function renderHTPDABreadcrumbs(data) {
    const stage = data.stage || 'STAGE_SETTLEMENT';
    const routine = data.routine || 'ROUTINE_PRIMARY_DUTY';
    const stack = data.activity_stack || []; // ADR-036 single activity stack, bottom first

    breadcrumbsContainer.innerHTML = '';

    // Base Stage & Routine
    const baseStageEl = document.createElement('span');
    baseStageEl.className = 'breadcrumb-item base';
    baseStageEl.innerHTML = `Stage: <strong>${formatStateName(stage)}</strong>`;
    breadcrumbsContainer.appendChild(baseStageEl);

    const sep1 = document.createElement('span');
    sep1.className = 'breadcrumb-sep';
    sep1.textContent = '›';
    breadcrumbsContainer.appendChild(sep1);

    const baseRoutineEl = document.createElement('span');
    baseRoutineEl.className = 'breadcrumb-item routine';
    baseRoutineEl.innerHTML = `Routine: <strong>${formatStateName(routine)}</strong>`;
    breadcrumbsContainer.appendChild(baseRoutineEl);

    // Render activity stack frames (bottom first)
    if (stack && stack.length > 0) {
      stack.forEach((frame) => {
        const sep = document.createElement('span');
        sep.className = 'breadcrumb-sep';
        sep.textContent = '›';
        breadcrumbsContainer.appendChild(sep);

        const frameEl = document.createElement('span');
        const stName = frame.kind || 'activity';
        const cls = getPreemptClass(stName);
        frameEl.className = `breadcrumb-item ${cls}`;
        frameEl.innerHTML = `Frame: <strong>${formatStateName(stName)}</strong>`;
        frameEl.title = `Frame #${frame.id || 0} (Start Tick: ${frame.start_tick || 0})`;
        breadcrumbsContainer.appendChild(frameEl);
      });

      stackDepthBadge.textContent = `Stack Depth: ${stack.length}`;
      stackDepthBadge.className = 'badge badge-purple';
    } else {
      stackDepthBadge.textContent = 'Stack Depth: 0';
      stackDepthBadge.className = 'badge';
    }

    // Transition Reason
    if (data.transition_reason) {
      transitionReason.textContent = data.transition_reason;
    } else if (stack.length > 0 && stack[stack.length - 1].reason) {
      transitionReason.textContent = stack[stack.length - 1].reason;
    } else {
      transitionReason.textContent = `Operating normally in ${formatStateName(routine)}`;
    }
  }

  function formatStateName(raw) {
    if (!raw) return '';
    return raw
      .replace(/^STAGE_/, '')
      .replace(/^ROUTINE_/, '')
      .replace(/_/g, ' ')
      .toLowerCase()
      .split(' ')
      .map(s => s.charAt(0).toUpperCase() + s.slice(1))
      .join(' ');
  }

  function getPreemptClass(name) {
    const upper = (name || '').toUpperCase();
    if (upper.includes('EMERGENCY') || upper.includes('FLEE') || upper.includes('COMBAT') || upper.includes('THREAT')) {
      return 'preempt-emergency';
    }
    if (upper.includes('DIALOGUE') || upper.includes('CHAT') || upper.includes('TRADE')) {
      return 'preempt-dialogue';
    }
    if (upper.includes('SHELTER') || upper.includes('REST') || upper.includes('NIGHT')) {
      return 'preempt-shelter';
    }
    return 'preempt-emergency';
  }

  // Render Circadian Clock
  function renderCircadianClock(data) {
    const tod = data.time_of_day != null ? data.time_of_day : 6000;
    const worldTick = data.world_tick || 0;

    // In Minecraft: tick 0 is 06:00, tick 6000 is 12:00, tick 18000 is 00:00
    const normTod = ((tod % 24000) + 24000) % 24000;
    const totalMinutes = Math.floor(((normTod + 6000) % 24000) * 1440 / 24000);
    const hours = Math.floor(totalMinutes / 60);
    const mins = totalMinutes % 60;
    const hh = String(hours).padStart(2, '0');
    const mm = String(mins).padStart(2, '0');
    mcDigitalTime.textContent = `${hh}:${mm}`;

    const dayNum = Math.floor(worldTick / 24000) + 1;
    mcDayCount.textContent = `Day ${dayNum}`;
    mcTicks.textContent = `Tick ${normTod.toLocaleString()} / 24,000`;

    // Cursor position (0% - 100%)
    const pct = (normTod / 24000) * 100;
    shiftCursor.style.left = `${pct}%`;

    // Shift Badge
    const shift = (data.shift || 'FIRST').toUpperCase();
    circadianShiftBadge.textContent = shift.includes('SHIFT') ? shift : `${shift} SHIFT`;

    // Environment Weather
    const weather = (data.weather || 'WEATHER_CLEAR').toUpperCase();
    if (weather.includes('THUNDER')) {
      circadianWeather.textContent = 'THUNDER ⚡';
    } else if (weather.includes('RAIN')) {
      circadianWeather.textContent = 'RAIN 🌧️';
    } else {
      circadianWeather.textContent = 'CLEAR ☀️';
    }

    // Tavern Social Window (11000 - 13000)
    const isTavern = normTod >= 11000 && normTod < 13000;
    circadianTavern.textContent = isTavern ? 'ACTIVE (Dusk Social)' : 'INACTIVE';
    circadianTavern.style.color = isTavern ? 'var(--warning)' : 'var(--text-muted)';

    // Dusk / Night (11000 - 23500)
    const isDuskNight = normTod >= 11000 && normTod < 23500;
    circadianDusk.textContent = isDuskNight ? 'DUSK / NIGHT 🌙' : 'DAYLIGHT ☀️';
  }

  // Render Settler Vitals
  function renderVitals(data) {
    const health = data.health != null ? data.health : 20.0;
    const maxHealth = data.max_health != null && data.max_health > 0 ? data.max_health : 20.0;
    const food = data.food_level != null ? data.food_level : 20;
    const sat = data.saturation != null ? data.saturation.toFixed(1) : '5.0';
    const stamina = data.stamina != null ? data.stamina : 100.0;
    const armor = data.armor_rating != null ? data.armor_rating : 0;

    // Health
    healthVal.textContent = `${health.toFixed(1)} / ${maxHealth.toFixed(1)} HP`;
    const healthPct = Math.max(0, Math.min(100, (health / maxHealth) * 100));
    healthBar.style.width = `${healthPct}%`;

    // Food
    foodVal.textContent = `${food} / 20 (Sat: ${sat})`;
    const foodPct = Math.max(0, Math.min(100, (food / 20) * 100));
    foodBar.style.width = `${foodPct}%`;

    // Stamina
    staminaVal.textContent = `${stamina.toFixed(1)}%`;
    staminaBar.style.width = `${stamina}%`;

    // Armor
    armorRatingBadge.textContent = `🛡️ ${armor} / 20 Armor`;

    // Position & Pose
    if (data.position) {
      posCoords.textContent = `X: ${data.position.x.toFixed(1)}, Y: ${data.position.y.toFixed(1)}, Z: ${data.position.z.toFixed(1)}`;
    } else {
      posCoords.textContent = 'X: 0.0, Y: 0.0, Z: 0.0';
    }

    const yaw = data.yaw != null ? data.yaw : 0;
    const pitch = data.pitch != null ? data.pitch : 0;
    const cardinal = getCardinalDirection(yaw);
    posFacing.textContent = `Yaw: ${yaw.toFixed(1)}° (${cardinal}), Pitch: ${pitch.toFixed(1)}°`;

    envBiome.textContent = data.biome_id || 'minecraft:plains';
    navState.textContent = (data.navigation_state || 'IDLE').replace(/^NAVIGATION_STATE_/, '');

    // Utility Scores
    const u = data.utility_scores || {};
    setUtilityBar(uHunger, uHungerVal, u.hunger != null ? u.hunger : 0.0);
    setUtilityBar(uThreat, uThreatVal, u.threat != null ? u.threat : 0.0);
    setUtilityBar(uConfidence, uConfidenceVal, u.confidence != null ? u.confidence : 0.5);
    setUtilityBar(uExplore, uExploreVal, u.exploration != null ? u.exploration : 0.2);
    setUtilityBar(uSleep, uSleepVal, u.sleep_need != null ? u.sleep_need : 0.0);
  }

  function setUtilityBar(barEl, valEl, val) {
    const clamped = Math.max(0, Math.min(1, val));
    barEl.style.width = `${clamped * 100}%`;
    valEl.textContent = clamped.toFixed(2);
  }

  function getCardinalDirection(yaw) {
    const norm = ((yaw % 360) + 360) % 360;
    if (norm >= 337.5 || norm < 22.5) return 'S';
    if (norm >= 22.5 && norm < 67.5) return 'SW';
    if (norm >= 67.5 && norm < 112.5) return 'W';
    if (norm >= 112.5 && norm < 157.5) return 'NW';
    if (norm >= 157.5 && norm < 202.5) return 'N';
    if (norm >= 202.5 && norm < 247.5) return 'NE';
    if (norm >= 247.5 && norm < 292.5) return 'E';
    return 'SE';
  }

  // Render Inventory Matrix
  function renderInventory(data) {
    const items = data.inventory || [];
    let occupiedCount = 0;

    // Reset all slots
    const allSlots = document.querySelectorAll('.inv-slot');
    allSlots.forEach(el => {
      el.className = 'inv-slot';
      if (el.dataset.slot === '0') el.classList.add('main-hand');
      if (el.id.startsWith('slot-3') || el.id === 'slot-40') {
        // preserve placeholder glyphs for armor & offhand
        const placeholder = el.querySelector('.slot-placeholder');
        el.innerHTML = '';
        if (placeholder) el.appendChild(placeholder);
      } else {
        el.innerHTML = '';
      }
      el.onmouseenter = null;
      el.onmousemove = null;
      el.onmouseleave = null;
    });

    items.forEach(it => {
      if (!it || it.count <= 0) return;
      occupiedCount++;

      const slotEl = document.getElementById(`slot-${it.slot}`);
      if (!slotEl) return;

      slotEl.classList.add('occupied');
      slotEl.innerHTML = '';

      // Item Name Glyph
      const glyphEl = document.createElement('span');
      glyphEl.className = 'item-glyph';
      glyphEl.textContent = getItemAbbr(it.item_id);
      slotEl.appendChild(glyphEl);

      // Stack Count Badge
      if (it.count > 1) {
        const countEl = document.createElement('span');
        countEl.className = 'item-count';
        countEl.textContent = it.count;
        slotEl.appendChild(countEl);
      }

      // Durability Bar
      if (it.max_durability && it.max_durability > 0) {
        const durBar = document.createElement('div');
        durBar.className = 'item-durability-bar';
        const pct = Math.max(0, Math.min(100, (it.durability / it.max_durability) * 100));
        durBar.style.width = `${pct}%`;
        if (pct < 20) durBar.style.backgroundColor = 'var(--danger)';
        else if (pct < 50) durBar.style.backgroundColor = 'var(--warning)';
        slotEl.appendChild(durBar);
      }

      attachSlotTooltip(slotEl, it);
    });

    invCountBadge.textContent = `${occupiedCount} / 36 slots`;
  }

  function getItemAbbr(rawID) {
    if (!rawID) return '';
    const name = rawID.includes(':') ? rawID.split(':')[1] : rawID;
    const parts = name.split('_');
    if (parts.length === 1) return parts[0].substring(0, 4).toUpperCase();
    return (parts[0].substring(0, 2) + parts[1].substring(0, 2)).toUpperCase();
  }

  // Render FactGraph Blackboard
  function renderBlackboard(factsMap) {
    if (!factsMap || Object.keys(factsMap).length === 0) {
      factsList.innerHTML = '<div class="empty-state">No facts asserted on blackboard</div>';
      factsCountBadge.textContent = '0 Facts';
      return;
    }

    const facts = Object.values(factsMap);
    const filter = (blackboardSearch.value || '').trim().toLowerCase();

    // Group by category
    const categorized = {};
    let totalCount = 0;

    facts.forEach(f => {
      if (filter) {
        const matchesKey = f.key && f.key.toLowerCase().includes(filter);
        const matchesEv = f.evidence && f.evidence.toLowerCase().includes(filter);
        const matchesCat = f.category && f.category.toLowerCase().includes(filter);
        if (!matchesKey && !matchesEv && !matchesCat) return;
      }

      const cat = f.category || 'General';
      if (!categorized[cat]) categorized[cat] = [];
      categorized[cat].push(f);
      totalCount++;
    });

    factsCountBadge.textContent = `${totalCount} Facts`;
    factsList.innerHTML = '';

    if (totalCount === 0) {
      factsList.innerHTML = '<div class="empty-state">No facts matching filter</div>';
      return;
    }

    for (const [catName, catFacts] of Object.entries(categorized)) {
      const groupEl = document.createElement('div');
      groupEl.className = 'fact-category-group';

      const titleEl = document.createElement('div');
      titleEl.className = 'fact-category-title';
      titleEl.textContent = `${catName} (${catFacts.length})`;
      groupEl.appendChild(titleEl);

      catFacts.forEach(fact => {
        const cardEl = document.createElement('div');
        cardEl.className = 'fact-card';

        const headerEl = document.createElement('div');
        headerEl.className = 'fact-header';

        const keyEl = document.createElement('span');
        keyEl.className = 'fact-key';
        keyEl.textContent = fact.key;
        headerEl.appendChild(keyEl);

        const valBadge = document.createElement('span');
        valBadge.className = 'fact-val-badge';

        if (fact.value) {
          if (typeof fact.value.bool === 'boolean') {
            valBadge.textContent = fact.value.bool ? 'TRUE' : 'FALSE';
            valBadge.classList.add(fact.value.bool ? 'true' : 'false');
          } else if (fact.value.int != null && fact.value.int !== 0) {
            valBadge.textContent = fact.value.int;
            valBadge.classList.add('other');
          } else if (fact.value.float != null && fact.value.float !== 0) {
            valBadge.textContent = fact.value.float.toFixed(2);
            valBadge.classList.add('other');
          } else if (fact.value.string) {
            valBadge.textContent = fact.value.string;
            valBadge.classList.add('other');
          } else {
            valBadge.textContent = 'SET';
            valBadge.classList.add('other');
          }
        }
        headerEl.appendChild(valBadge);
        cardEl.appendChild(headerEl);

        if (fact.evidence) {
          const evEl = document.createElement('div');
          evEl.className = 'fact-evidence';
          evEl.textContent = fact.evidence;
          cardEl.appendChild(evEl);
        }

        groupEl.appendChild(cardEl);
      });

      factsList.appendChild(groupEl);
    }
  }

  // Render System 2 Reflections
  function renderReflections(data) {
    const goals = data.goals || {};
    goalImmediate.textContent = goals.immediate_goal || 'None (Reactive loop)';
    goalShort.textContent = goals.short_term_goal || 'None';
    goalLong.textContent = goals.long_term_goal || 'None';

    const refl = data.reflections || {};
    if (refl.latest_thought) {
      reflectionContemplation.textContent = `"${refl.latest_thought}"`;
    } else {
      reflectionContemplation.textContent = 'No reflective memories recorded yet. System 2 contemplates during twilight tavern social and dawn prep.';
    }

    if (refl.latest_dialogue) {
      reflectionDialogue.textContent = refl.latest_dialogue;
    } else {
      reflectionDialogue.textContent = 'No recent dialogue exchanges.';
    }

    if (refl.latest_chronicle) {
      reflectionChronicle.textContent = refl.latest_chronicle;
    } else {
      reflectionChronicle.textContent = 'Chronicler has not recorded annals entries yet.';
    }

    const activeLLM = data.active_llm_requests != null ? data.active_llm_requests : 0;
    llmRequestsBadge.textContent = `LLM Reqs: ${activeLLM}`;
  }

  // Event Listeners
  agentSelect.addEventListener('change', (e) => {
    selectAgent(e.target.value);
  });

  btnRefresh.addEventListener('click', () => {
    fetchAgents();
  });

  blackboardSearch.addEventListener('input', () => {
    if (latestTelemetry && latestTelemetry.blackboard) {
      renderBlackboard(latestTelemetry.blackboard);
    }
  });

  // Initialization
  initInventoryGrid();
  fetchAgents();

  // Periodically refresh agent roster every 5 seconds
  setInterval(fetchAgents, 5000);

})();
