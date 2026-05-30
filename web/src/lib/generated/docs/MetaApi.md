# MetaApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**getOpenAPISpec**](MetaApi.md#getopenapispec) | **GET** /api/v1/openapi.json | Return this OpenAPI document as JSON. |



## getOpenAPISpec

> object getOpenAPISpec()

Return this OpenAPI document as JSON.

The exact same spec embedded into the binary at build time, rendered as JSON for tools that don\&#39;t speak YAML. The response is bytewise stable for a given build (no per-request server URL substitution). 

### Example

```ts
import {
  Configuration,
  MetaApi,
} from '';
import type { GetOpenAPISpecRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new MetaApi();

  try {
    const data = await api.getOpenAPISpec();
    console.log(data);
  } catch (error) {
    console.error(error);
  }
}

// Run the test
example().catch(console.error);
```

### Parameters

This endpoint does not need any parameter.

### Return type

**object**

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | The OpenAPI 3.0 document. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

