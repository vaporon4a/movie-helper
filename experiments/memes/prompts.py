"""Frozen v1 prompts; no examples from the scored evaluation set."""
import json
from pathlib import Path
VARIANTS=json.loads(Path(__file__).with_name('frozen-prompts.json').read_text())
