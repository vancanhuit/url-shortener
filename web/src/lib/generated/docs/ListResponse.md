
# ListResponse


## Properties

Name | Type
------------ | -------------
`items` | [Array&lt;LinkResponse&gt;](LinkResponse.md)
`next_cursor` | number

## Example

```typescript
import type { ListResponse } from ''

// TODO: Update the object below with actual values
const example = {
  "items": null,
  "next_cursor": null,
} satisfies ListResponse

console.log(example)

// Convert the instance to a JSON string
const exampleJSON: string = JSON.stringify(example)
console.log(exampleJSON)

// Parse the JSON string back to an object
const exampleParsed = JSON.parse(exampleJSON) as ListResponse
console.log(exampleParsed)
```

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


