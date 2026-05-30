# OperationalApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**livez**](OperationalApi.md#livez) | **GET** /livez | Liveness probe. |
| [**readyz**](OperationalApi.md#readyz) | **GET** /readyz | Readiness probe. |
| [**version**](OperationalApi.md#version) | **GET** /version | Build metadata. |



## livez

> Livez200Response livez()

Liveness probe.

Returns &#x60;200&#x60; as long as the process is running and the HTTP stack is responsive. Has no dependencies on Postgres or Redis -- a flapping database must not cause Kubernetes to restart the pod. 

### Example

```ts
import {
  Configuration,
  OperationalApi,
} from '';
import type { LivezRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new OperationalApi();

  try {
    const data = await api.livez();
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

[**Livez200Response**](Livez200Response.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | The process is alive. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## readyz

> ReadyzResponse readyz()

Readiness probe.

Pings every registered dependency (Postgres, Redis) with a short per-check timeout and returns &#x60;200&#x60; only when all succeed. The body lists per-check results so operators can see which dependency is unhappy. Flips to &#x60;503&#x60; during graceful shutdown so a load balancer drains the pod cleanly before the listener closes. 

### Example

```ts
import {
  Configuration,
  OperationalApi,
} from '';
import type { ReadyzRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new OperationalApi();

  try {
    const data = await api.readyz();
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

[**ReadyzResponse**](ReadyzResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | All readiness checks passed. |  -  |
| **503** | One or more readiness checks failed, or the server is shutting down. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## version

> VersionResponse version()

Build metadata.

Returns the &#x60;version&#x60;, &#x60;commit&#x60;, and &#x60;date&#x60; baked into the binary at build time via &#x60;-ldflags&#x60;.

### Example

```ts
import {
  Configuration,
  OperationalApi,
} from '';
import type { VersionRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new OperationalApi();

  try {
    const data = await api.version();
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

[**VersionResponse**](VersionResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | Build metadata. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

