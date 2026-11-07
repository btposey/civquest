# Progressive Discovery: System 2 Deliberative LLM Pipelines

## 1. Gateway Architecture & Inference Governor
System 2 handles deliberative, social, creative, and planning tasks through an OpenAI-compatible local API (Ollama or vLLM):
- **Concurrency Control**: Worker pool strictly limited by `max_concurrent_llm_requests` (default: 4).
- **Timeout**: Strict per-request timeout (default: 30 seconds).
- **Dual-Temperature Sampling**:
  - $T = 0.10\text{--}0.20$: Deterministic structured generation (Architect BOM, quadrant compilation, task delegation).
  - $T = 0.85\text{--}1.05$: Creative generation (Dialogue, trade haggling, Chronicler books, toponyms).

## 2. Pipeline A: Dialogue & Barter Haggling ($T = 0.85$)
Triggered by `ChatEvent` or `TradeRequest`:
- **Context Assembled**: Agent identity, personality quirks, current vitals, clan food reserves (`DEFICIT`, `STABLE`, `SURPLUS`), player's `prestige_score`, and recent episodic memories retrieved via `sqlite-vec`.
- **Structured JSON Output**:
  ```json
  {
    "spoken_reply": "string",
    "trade_decision": "ACCEPT | COUNTER | REJECT | NONE",
    "counter_offer_items": [{"item_id": "string", "count": 0}],
    "delegated_task": {
      "task_type": "NONE | ESCORT_PLAYER | GATHER_RESOURCE | SHARE_CROP_PROPOSAL",
      "target_block_or_item": "string",
      "quota": 0
    },
    "prestige_delta": 0.0,
    "memory_summary": "string"
  }
  ```
- **Stochastic Disobedience**: Agents refuse dangerous orders from high-prestige players if a random roll exceeds their `Bravery` trait or if the clan has food deficits.

## 3. Pipeline B: The Clan Chronicler ($T = 0.90$)
Triggered at dusk when a `CHRONICLER` is seated at a library lectern and `episodic_memories` has unchronicled events with `saliency_score >= 0.70` (or `1.0` for permadeath).
- **Pagination Formatter (`PaginateProse`)**:
  - Enforces strict vanilla Minecraft written book limits:
    - Title: $\le 32$ characters.
    - Page: $\le 14$ lines, $\le 19$ characters per line, $\le 250$ total characters.
    - Max pages: 100.
  - Emits `CreateWrittenBookIntent` to insert the book into a target Lectern or Chiseled Bookshelf.

## 4. Pipeline C: Toponym & Landmark Naming ($T = 0.85$)
Triggered upon discovering a new biome, consecrated building, or fallen hero site:
- Generates cultural name ($\le 24$ chars), 2 signpost lines ($\le 15$ chars each), and historical lore.
- Emits `PlaceSignpostIntent` to erect a physical sign in the world.

## 5. Pipeline D: Architect Quadrant Construction ($T = 0.20$)
- Parses blueprint YAML/NBT.
- Calculates Bill of Materials (BOM) against granary stock.
- Subdivides build into 4 spatial quadrants ($NW, NE, SW, SE$).
- Emits bottom-up voxel intents ($Y_{\text{min}} \to Y_{\text{max}}$).
