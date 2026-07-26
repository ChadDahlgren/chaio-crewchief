# Amazon Bedrock (OpenAI-compatible endpoint)
1. Console → Bedrock → API keys → create one. Env: `AWS_BEARER_TOKEN_BEDROCK`.
2. Preset: `base_url: https://bedrock-mantle.<region>.api.aws`,
   `model_id: "openai.gpt-oss-120b"` (or e.g. "us.anthropic.claude-sonnet-4-6"),
   `api_key_env: AWS_BEARER_TOKEN_BEDROCK`, `health_path: /v1/models`,
   `provider_class: cloud`.
3. Claude 4.6+ models on Bedrock reject the temperature param: add
   `omit_temperature: true` to those presets.
The legacy `bedrock-runtime.<region>.amazonaws.com/v1` endpoint also accepts
Bedrock API keys as Bearer tokens if mantle isn't in your region.
