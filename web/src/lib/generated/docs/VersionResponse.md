
# VersionResponse


## Properties

Name | Type
------------ | -------------
`version` | string
`commit` | string
`date` | Date

## Example

```typescript
import type { VersionResponse } from ''

// TODO: Update the object below with actual values
const example = {
  "version": v0.4.0,
  "commit": dec456d,
  "date": 2026-05-06T11:30Z,
} satisfies VersionResponse

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as VersionResponse
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


