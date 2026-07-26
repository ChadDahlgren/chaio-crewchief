# Anthropic (frontier reference)
Env: `ANTHROPIC_API_KEY`.
Preset: `base_url: https://api.anthropic.com`, `model_id: "claude-sonnet-5"`,
`api_key_env: ANTHROPIC_API_KEY`, `health_path: /v1/models`,
`omit_temperature: true` (Claude 4.6+ rejects temperature),
`provider_class: frontier`.
Note: the health probe shows unhealthy (`/v1/models` wants x-api-key auth) but
delegations work — the chat completions endpoint accepts Bearer.
