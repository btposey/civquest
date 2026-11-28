#!/usr/bin/env python3
"""Per-agent report on a sandbox run (scripts/live-agent-smoke.sh, scripts/sandbox-playtest.sh).

Usage: live-report.py MODE EVENTS SERVER_LOG END CHAT_AGENT MODEL START HUNGER NIGHT AGENT...
Times are UTC, YYYY-MM-DDTHH:MM:SS; arguments a mode does not use may be empty.
"""
import json, re, sys, collections, datetime as dt
# mode "smoke" judges the run (exit status 1 on any failure). mode "playtest" only summarises it:
# nothing was forced, so there is no hunger phase or chat to check, and the loop checks, whose
# thresholds were set for the short smoke, are listed as flags to look into.
mode, events_path, server_log, end_iso, chat_agent, model, start_iso, hungry_iso, night_iso, *agents = sys.argv[1:]
smoke = mode == "smoke"
end = dt.datetime.fromisoformat(end_iso)
duration = (end - dt.datetime.fromisoformat(start_iso)).total_seconds()
when = lambda e: dt.datetime.fromisoformat(e["timestamp"][:19])

def loops(ev):
    """Longest emergency (s), most pushes of one activity, worst motionless repeat, bed tries, lowest food."""
    pushes, repeats = collections.Counter(), collections.Counter()
    emergency = longest = beds = 0.0
    since = here = None
    food, worst = 20, (0, "")
    for e in sorted(ev, key=lambda e: e["timestamp"]):
        d, c = e.get("details") or {}, e["category"]
        if c == "VITALS":
            food = min(food, d.get("food", 20))
            if d.get("stage") == "EMERGENCY":
                since = since or when(e)
                longest = max(longest, (when(e) - since).total_seconds())
            else:
                since = None
            p = d.get("position")
            if p and (here is None or ((p[0]-here[0])**2 + (p[1]-here[1])**2 + (p[2]-here[2])**2) ** .5 > 1.0):
                here, repeats = p, collections.Counter()
        elif c == "FSM_FRAME" and d.get("op") == "push":
            pushes[d.get("kind")] += 1
        elif c == "INTENT_OUT":
            action = d.get("action", "")
            beds += "SLEEP_IN_BED" in action
            # Mining one block takes several swings; chat is not movement or work.
            if "HARVEST_BREAK" in action or "Chat" in action or "Attack" in action:
                continue
            key = f"{action} {d.get('target')}"
            repeats[key] += 1
            if repeats[key] > worst[0]:
                worst = (repeats[key], key)
    kind, n = max(pushes.items(), key=lambda kv: kv[1], default=("", 0))
    return longest, (n, kind), worst, int(beds), food

def watchdog(ev):
    """The agent's own report of being stuck (WATCHDOG events), one line per condition and activity."""
    seen = {}
    for e in sorted(ev, key=lambda e: e["timestamp"]):
        if e["category"] != "WATCHDOG":
            continue
        d = e.get("details") or {}
        key = (d.get("condition", "?"), d.get("activity", "?"))
        first = seen.setdefault(key, {"n": 0, "tick": e.get("world_tick"), "why": d.get("why", ""), "attempting": d.get("attempting", "")})
        first["n"] += 1
    return [f"WATCHDOG {c} on {act}, reported {f['n']}x, first at tick {f['tick']}: {f['why']}; attempting: {f['attempting']}"
            for (c, act), f in seen.items()]

def frozen(ev):
    """Longest time (s) an agent claimed to be walking while its position never changed."""
    walking, at, since, longest = False, None, None, 0.0
    for e in sorted(ev, key=lambda e: e["timestamp"]):
        d, c = e.get("details") or {}, e["category"]
        if c == "MOTION":
            walking = d.get("navigation_state") == "NAVIGATION_STATE_PATHING"
            if not walking:
                since = None
        elif c == "VITALS" and d.get("position"):
            here = tuple(round(v, 1) for v in d["position"])
            if here != at or not walking:
                at, since = here, None
            else:
                since = since or when(e)
                longest = max(longest, (when(e) - since).total_seconds())
    return longest

by = collections.defaultdict(list)
for line in open(events_path):
    try: e = json.loads(line)
    except ValueError: continue
    by[e.get("agent_id")].append(e)
failed, stuck_flags = [], []
print(f"{'agent':8} {'events':>7} {'silent_s':>8} {'stalls':>6} {'stuck':>6} {'moved':>6} {'repeat':>6}  top crafts")
log = open(server_log, errors="replace").read()
deaths = dict(re.findall(r"AgentEntity died: agentId='([^']+)'.*?cause='([^']*)'", log))
# Dying to the world is the agent's own failure; losing a fight is reported but allowed.
SELF_INFLICTED = ("drowned", "flames", "burn", "lava", "fell", "hit the ground", "suffocated", "starved", "cactus", "pricked")
for a in agents:
    ev = by.get(a, [])
    if not ev:
        failed.append(f"{a}: no events"); continue
    silent = (end - when(ev[-1])).total_seconds()
    cat = collections.Counter(e["category"] for e in ev)
    pos = [e["details"]["position"] for e in ev if e["category"] == "VITALS" and e["details"].get("position")]
    moved = sum(((p[0]-q[0])**2 + (p[2]-q[2])**2) ** .5 for p, q in zip(pos, pos[1:]))
    crafts = collections.Counter(e["summary"].split("recipe=")[1].rstrip(")").replace("minecraft:", "")
                                 for e in ev if "recipe=" in e.get("summary", ""))
    targets = collections.Counter(tuple(e["details"]["target"]) for e in ev
                                  if e["category"] == "INTENT_OUT" and e["details"].get("target"))
    repeat = max(targets.values(), default=0)
    dead = a in deaths
    print(f"{a:8} {len(ev):7} {silent:8.0f} {cat['BRAIN_STALL']:6} {cat['MOTOR_STUCK']:6} {moved:6.0f} {repeat:6}  {dict(crafts.most_common(5))}" + (f" DIED: {deaths[a]}" if dead else ""))
    if cat["BRAIN_STALL"]: failed.append(f"{a}: brain stalled")
    if dead and any(w in deaths[a] for w in SELF_INFLICTED): failed.append(f"{a}: died ({deaths[a]})")
    if silent > 15 and not dead: failed.append(f"{a}: silent for the last {silent:.0f}s")
    for recipe, n in crafts.items():
        if n > 40: failed.append(f"{a}: crafted {recipe} {n} times (craft loop)")
    # Walking to one place re-sends it now and then; hundreds of times is a retry loop.
    if repeat > 200: failed.append(f"{a}: sent one movement target {repeat} times (retry loop)")
    stuck_walking = frozen(ev)
    if stuck_walking > 90: failed.append(f"{a}: walking on the spot for {stuck_walking:.0f}s (frozen body)")
    longest, (pushed, kind), (still, action), beds, food = loops(ev)
    print(f"{'':8} emergency {longest:.0f}s, {kind} pushed {pushed}x, {still}x unmoved, bed tries {beds}, lowest food {food}")
    if longest > 60: failed.append(f"{a}: in an emergency for {longest:.0f}s")
    # A real preemption now and then; one every few seconds is churn.
    if pushed > max(60, duration / 5): failed.append(f"{a}: pushed {kind} {pushed} times (frame churn)")
    if still > 60: failed.append(f"{a}: sent '{action}' {still} times without moving")
    if beds > 15: failed.append(f"{a}: tried to sleep {beds} times")
    if smoke and food > 12 and not dead: failed.append(f"{a}: never got hungry, the hunger phase did not work")
    # A watchdog firing is a bug: the agent was stuck and nothing rescued it (ADR-041 section 4).
    reports = [f"{a}: {line}" for line in watchdog(ev)]
    failed.extend(reports)
    stuck_flags.extend(reports)
fired = collections.Counter((e.get("details") or {}).get("condition", "?") for a in agents for e in by.get(a, []) if e["category"] == "WATCHDOG")
print(f"watchdog reports: {sum(fired.values())}" + "".join(f", {c} {n}" for c, n in sorted(fired.items())))
print(f"server: {log.count('Dropping telemetry frames')} backpressure warnings, {log.count(chr(39) + ' spoke in chat')} agent chat lines")
# A run on treeless ground (open ocean) exercises little: say so rather than pass quietly.
crafted = sum(1 for a in agents if any("recipe=" in e.get("summary", "") for e in by.get(a, [])))
everything = [e for a in agents for e in by.get(a, [])]
votes = sum(e["category"] == "SETTLEMENT_VOTE" for e in everything)
sites = sorted({e["summary"] for e in everything if e["category"] == "COLLECTIVE_SETTLEMENT_SITE"})
print(f"deaths: {len(deaths)}; crafted something: {crafted} of {len(agents)}; settlement votes: {votes}; " +
      (sites[0] if sites else "no settlement formed, so settled behaviour and nights near camp were not exercised"))
if crafted * 2 < len(agents):
    print(f"NOTE: only {crafted} of {len(agents)} agents crafted anything; wood gathering and crafting were barely exercised")
if not smoke:
    for a, cause in sorted(deaths.items()):
        print(f"died: {cause}")
    print(f"span (UTC): start {start_iso}, end {end_iso}")
    for f in failed: print("FLAG", f)
    sys.exit(0)
if model:
    replied = f"Agent '{chat_agent}' spoke in chat" in log
    if chat_agent in deaths:
        print(f"chat: {chat_agent} died before it could be asked, chat not checked")
    else:
        print(f"chat: {chat_agent} {'replied' if replied else 'did NOT reply'}")
        if not replied: failed.append(f"{chat_agent}: no chat reply")
else:
    print("chat: skipped (no model endpoint)")
print(f"phases (UTC): start {start_iso}, hunger {hungry_iso}, night {night_iso}, end {end_iso}")
for f in stuck_flags: print("FLAG", f)
for f in failed: print("FAIL", f)
print("RESULT:", "FAIL" if failed else "PASS")
sys.exit(1 if failed else 0)
