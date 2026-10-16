# Configured OpenAI Images template

This is MovieDesign-owned reference content. Logical read-only path:
knowledge/providers/openai-images.md; HTTP read: /api/knowledge/providers/openai-images.md.

Explicit configured base URL and model are used. A custom endpoint is a declaration
of compatibility, not proof that every provider implements this wire protocol.
Only the image operation is implemented. No paid connection probe is performed.

Prompt text is sent unchanged. Limits: at most 65,536 UTF-8 bytes and 32,000 Unicode
characters, or any tighter configured limits. Count defaults to one and must match
all declared output resources and roles. Params are flat, schema-whitelisted fields.
No model, prompt, n, image, mask, URL, path or base64 overrides are accepted.

The conservative synchronous template supports quality auto/low/medium/high,
background auto/opaque/transparent, moderation auto/low, PNG/JPEG output_format,
JPEG output_compression 0..100, safety_identifier <=128 characters, and standard
1024x1024 / 1536x1024 / 1024x1536 sizes. Config can only restrict these fields.
WebP, streaming, input_fidelity and model-specific extended sizes/qualities are not
implemented. Unknown fields fail explicitly; no model-name capability inference.

Without references: POST images/generations JSON. With references: POST images/edits
multipart; each image repeats image[] in the supplied asset-ID order, including
repeated IDs. If configured reference_roles is nonempty, every position must be
provided exactly. Only image and mask roles exist; at most one mask, after the
first image. The mask is sent as mask rather than image[]. Otherwise all references
are images. Input PNG/JPEG <=20 MiB and <=16,777,216 pixels (config may tighten).

A mask is a real managed PNG asset with the same dimensions as the first image.
Transparent pixels mark areas to edit. POST /api/generate/mask accepts source-pixel
rectangles from M3.4 annotations, uses opaque pixels outside and transparent pixels
inside the rectangles, and registers the result through workspace upload/commit.
Use its asset_id in the configured mask position. The model may alter content
outside the mask; exact preservation is not guaranteed. No raw filesystem access
or base64 parameter is supported.

Quotes are estimates from user-configured pricing, never claims of upstream billing
accuracy. Explicit zero pricing is allowed; absent/future pricing blocks generation.
Every task remains at human billing approval until M4.4 supplies a real UI receipt.
A timeout after POST is unknown and must never automatically resubmit. A saved
response is collected without another generation request; final task output
identity and Delivery integration belong to M4.5.

Official wire references verified 2026-10-16:
https://developers.openai.com/api/reference/resources/images/methods/generate
https://developers.openai.com/api/reference/resources/images/methods/edit
https://developers.openai.com/api/docs/guides/image-generation
Verification here is documentation and httptest wire evidence, not real-provider
quality, billing, latency or compatibility acceptance.
