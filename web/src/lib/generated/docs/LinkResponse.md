
# LinkResponse


## Properties

Name | Type
------------ | -------------
`code` | string
`short_url` | string
`target_url` | string
`created_at` | Date
`click_count` | number
`expires_at` | Date

## Example

```typescript
import type { LinkResponse } from ''

// TODO: Update the object below with actual values
const example = {
  "code": abc1234,
  "short_url": http://localhost:8080/r/abc1234,
  "target_url": https://example.com/long/path,
  "created_at": 2026-05-06T11:30Z,
  "click_count": 0,
  "expires_at": 2026-12-31T23:59:59Z,
} satisfies LinkResponse

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as LinkResponse
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


