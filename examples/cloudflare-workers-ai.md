# Cloudflare Workers AI
1. Dashboard → My Profile → API Tokens → create token with Workers AI (Read+Run).
2. Env for the gateway: `CF_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`.
3. Preset: `base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai`,
   `model_id: "@cf/openai/gpt-oss-120b"` (any catalog text model),
   `api_key_env: CLOUDFLARE_API_TOKEN`, `health_path: /models/search`,
   `provider_class: cloud`.
Free tier: 10,000 neurons/day. Pricing per model at developers.cloudflare.com/workers-ai/platform/pricing.
