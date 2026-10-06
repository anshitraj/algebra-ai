# Cloud Vision API
> Analyze images with Google Cloud Vision ML. Detect objects, faces, text/OCR, labels, landmarks, logos, safe-search signals, web entities, crop hints, dominant colors, and product-search reference images across image, PDF, and TIFF inputs.

## Agent Summary
- FQN: `solana-foundation/google/vision`
- Category: ai_ml
- Operator: solana-foundation
- Origin: google
- Version: v1
- Endpoints: 2
- Pricing: $0.0015
- HTML page: https://pay.sh/api/solana-foundation/google/vision
- Markdown page: https://pay.sh/api/solana-foundation/google/vision/index.md

## Service URLs
- Gateway: https://vision.google.gateway-402.com
- Source: solana-foundation/pay-skills
- Source path: providers/solana-foundation/google/vision/PAY.md
- Source skill: pay-skills

## Use Case
Use for OCR, image labeling, object and face detection, content moderation, logo and landmark recognition, product search, PDF/TIFF text extraction, web entity lookup, crop hints, color analysis, and visual metadata enrichment.

## Endpoint Table
| Method | Path | Pricing | Description |
| --- | --- | --- | --- |
| POST | v1/images:annotate | $0.0015/requests | Run image detection and annotation for a batch of images. |
| POST | v1/images:asyncBatchAnnotate | $0.0015/requests | Run asynchronous image detection and annotation for a list |
