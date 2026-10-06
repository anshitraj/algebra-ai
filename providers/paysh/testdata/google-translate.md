# Cloud Translation API
> Translate text and documents across 130+ languages. Supports language detection, romanization, synchronous and batch translation, glossaries for domain terminology, adaptive MT datasets, custom models, and document formatting.

## Agent Summary
- FQN: `solana-foundation/google/translate`
- Category: translation
- Operator: solana-foundation
- Origin: google
- Version: v3
- Endpoints: 7
- Pricing: $0.001-$0.08
- HTML page: https://pay.sh/api/solana-foundation/google/translate
- Markdown page: https://pay.sh/api/solana-foundation/google/translate/index.md

## Service URLs
- Gateway: https://translate.google.gateway-402.com
- Source: solana-foundation/pay-skills
- Source path: providers/solana-foundation/google/translate/PAY.md
- Source skill: pay-skills

## Use Case
Use for multilingual content, localization, document translation, language detection, cross-language communication, glossary-controlled terminology, romanization, batch translation jobs, adaptive MT, and custom translation models.

## Endpoint Table
| Method | Path | Pricing | Description |
| --- | --- | --- | --- |
| POST | v3/projects/{projectsId}/locations/{locationsId}:detectLanguage | $0.001/requests | Detects the language of text within a request. |
| POST | v3/projects/{projectsId}/locations/{locationsId}:translateDocument | $0.08/requests | Translates documents in synchronous mode. |
| POST | v3/projects/{projectsId}/locations/{locationsId}:translateText | $0.001/requests | Translates input text and returns translated text. |
| GET | v3/projects/{projectsId}/supportedLanguages | free | Returns a list of supported languages for translation. |
| POST | v3/projects/{projectsId}:detectLanguage | $0.001/requests | Detects the language of text within a request. |
| POST | v3/projects/{projectsId}:romanizeText | free | Romanize input text written in non-Latin scripts to Latin text. |
| POST | v3/projects/{projectsId}:translateText | free | Translates input text and returns translated text. |
