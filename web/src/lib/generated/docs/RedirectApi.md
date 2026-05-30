# RedirectApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**redirectLink**](RedirectApi.md#redirectlink) | **GET** /r/{code} | Redirect to the link\&#39;s target URL. |



## redirectLink

> redirectLink(code)

Redirect to the link\&#39;s target URL.

The public short-URL endpoint. On a hit, returns &#x60;302 Found&#x60; with &#x60;Location: &lt;target_url&gt;&#x60; and fires a fire-and-forget click-counter increment that never delays the response. Hot path is served from a Redis read-through cache; the cache TTL is clamped to the link\&#39;s remaining lifetime so an expired link can never be served from cache.  Soft-deleted and expired links return &#x60;410 Gone&#x60;. The response body is plain text; programmatic clients that need the machine-readable &#x60;code&#x60; field should call &#x60;GET /api/v1/links/{code}&#x60; instead. 

### Example

```ts
import {
  Configuration,
  RedirectApi,
} from '';
import type { RedirectLinkRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new RedirectApi();

  const body = {
    // string | The short code identifying the link. Must match `^[0-9A-Za-z]{4,64}$`; codes that fail this shape check are rejected with `404` rather than `422` so the API doesn\'t leak its validation rules through the path parameter. 
    code: abc1234,
  } satisfies RedirectLinkRequest;

  try {
    const data = await api.redirectLink(body);
    console.log(data);
  } catch (error) {
    console.error(error);
  }
}

// Run the test
example().catch(console.error);
```

### Parameters


| Name | Type | Description  | Notes |
|------------- | ------------- | ------------- | -------------|
| **code** | `string` | The short code identifying the link. Must match &#x60;^[0-9A-Za-z]{4,64}$&#x60;; codes that fail this shape check are rejected with &#x60;404&#x60; rather than &#x60;422&#x60; so the API doesn\&#39;t leak its validation rules through the path parameter.  | [Defaults to `undefined`] |

### Return type

`void` (Empty response body)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `text/plain`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **302** | Redirect to the link\&#39;s &#x60;target_url&#x60;. |  * Location - The link\&#39;s &#x60;target_url&#x60;. <br>  |
| **404** | No link with this code exists, or the code is malformed.  |  -  |
| **410** | The link existed but is no longer active (deleted or expired). |  -  |
| **500** | Server-side failure looking up the link. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

