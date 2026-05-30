
# LinkRequest


## Properties

Name | Type
------------ | -------------
`target_url` | string
`code` | string
`expires_at` | Date

## Example

```typescript
import type { LinkRequest } from ''

// TODO: Update the object below with actual values
const example = {
  "target_url": https://example.com/long/path,
  "code": docs2026,
  "expires_at": 2026-12-31T23:59:59Z,
} satisfies LinkRequest

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as LinkRequest
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


