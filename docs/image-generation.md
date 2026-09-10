# Image Generation tool

Install **Image Generation** from Tools and enable it for the workspace or attach it to an agent. Ask for an image with a description. The generated image opens in the chat side panel and can be reopened or downloaded from its message.

The tool uses the current workspace gateway URL and encrypted API key, falling back to the process gateway only when the workspace has no gateway configuration. It calls the OpenAI-compatible `POST /images/generations` endpoint with `prompt`, `model`, `n: 1`, and optional `size`.

When `model` is omitted, discovery uses the gateway's `/model/info` response and selects the alphabetically first model with `model_info.mode = image_generation`. If the gateway does not publish that metadata, provide the exact image model ID in the request. No image model is assumed from the selected chat model. Gateways offering image generation only through chat or Responses endpoints are not supported by this tool.

PNG, JPEG, WebP and GIF responses are accepted, up to 8 MiB per image. Base64 responses are stored directly in the message's tool result. Provider URLs are fetched through the public-network egress guard without gateway credentials, then stored as image data so expiring URLs do not break old messages. Image data is excluded from the model's text history. Generation has a five-minute HTTP deadline and is not automatically retried after failure.

This version generates one new image per call; image editing is not included. Storage usage grows with the images retained in conversations.

API contract: [OpenAI image generation reference](https://developers.openai.com/api/reference/resources/images/methods/generate).
