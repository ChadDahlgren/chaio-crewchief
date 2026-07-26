# Local GPU via llama.cpp
Run llama-server with any coder model:
```bash
llama-server -m gpt-oss-120b.gguf --port 8080 -ngl 999
```
Preset: `base_url: http://localhost:8080`, no `model_id`, no `api_key_env`,
`provider_class: local` (default). llama-server ignores the model field and
reports `timings.predicted_per_second`, which the ledger records as tok/sec.
