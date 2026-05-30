# LinksApi

All URIs are relative to *http://localhost:8080*

| Method | HTTP request | Description |
|------------- | ------------- | -------------|
| [**createLink**](LinksApi.md#createlink) | **POST** /api/v1/links | Create a short link. |
| [**deleteLink**](LinksApi.md#deletelink) | **DELETE** /api/v1/links/{code} | Soft-delete a link. |
| [**getLink**](LinksApi.md#getlink) | **GET** /api/v1/links/{code} | Fetch link metadata. |
| [**listLinks**](LinksApi.md#listlinks) | **GET** /api/v1/links | List recent links. |



## createLink

> LinkResponse createLink(linkRequest)

Create a short link.

Creates a new short link for the supplied &#x60;target_url&#x60;. If &#x60;code&#x60; is omitted, the server generates a 7-character base62 code; if supplied, it must match &#x60;^[0-9A-Za-z]{4,64}$&#x60;.  When &#x60;code&#x60; is omitted *and* &#x60;expires_at&#x60; is omitted, the request is deduped against the existing rows: a previously shortened permanent link with the same normalized target URL is returned with &#x60;200 OK&#x60; instead of a fresh &#x60;201 Created&#x60; row. Callers requesting a custom code or an expiring link always opt out of dedup. 

### Example

```ts
import {
  Configuration,
  LinksApi,
} from '';
import type { CreateLinkRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new LinksApi();

  const body = {
    // LinkRequest
    linkRequest: {"target_url":"https://example.com/long/path"},
  } satisfies CreateLinkRequest;

  try {
    const data = await api.createLink(body);
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
| **linkRequest** | [LinkRequest](LinkRequest.md) |  | |

### Return type

[**LinkResponse**](LinkResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: `application/json`
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | Dedup hit -- the normalized &#x60;target_url&#x60; already exists as a permanent auto-generated link, returned unchanged.  |  -  |
| **201** | New link created. |  -  |
| **400** | Request body is not parseable JSON. |  -  |
| **409** | The user-supplied &#x60;code&#x60; is already in use. |  -  |
| **422** | Input failed validation (URL, code shape, or expiry). |  -  |
| **429** | Per-IP create budget exceeded. Only emitted when the server has rate limiting enabled (&#x60;URL_SHORTENER_RATE_LIMIT_RPS &gt; 0&#x60;); deployments fronted by an upstream rate limiter typically leave this off.  |  -  |
| **500** | Server-side failure; details are logged on the server, not surfaced. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## deleteLink

> deleteLink(code)

Soft-delete a link.

Marks the link as deleted by stamping an internal &#x60;deleted_at&#x60; tombstone. The row stays in storage so audit columns (&#x60;created_at&#x60;, &#x60;click_count&#x60;) survive, but subsequent redirect, lookup, list, and dedup operations stop seeing it. The cache entry, if any, is invalidated server-side so the next &#x60;GET /r/{code}&#x60; returns &#x60;410&#x60; immediately rather than waiting out the cache TTL.  Semantically idempotent (the row stays deleted) but response-distinct: the first DELETE returns &#x60;204&#x60;, the second returns &#x60;404&#x60;. Clients that need &#x60;204+204&#x60; can collapse the two themselves. 

### Example

```ts
import {
  Configuration,
  LinksApi,
} from '';
import type { DeleteLinkRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new LinksApi();

  const body = {
    // string | The short code identifying the link. Must match `^[0-9A-Za-z]{4,64}$`; codes that fail this shape check are rejected with `404` rather than `422` so the API doesn\'t leak its validation rules through the path parameter. 
    code: abc1234,
  } satisfies DeleteLinkRequest;

  try {
    const data = await api.deleteLink(body);
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
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **204** | Link successfully soft-deleted. No body. |  -  |
| **404** | The code does not exist (or is malformed). |  -  |
| **500** | Server-side failure; details are logged on the server, not surfaced. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## getLink

> LinkResponse getLink(code)

Fetch link metadata.

Returns the metadata for the link without performing a redirect or incrementing the click counter. Soft-deleted and expired links surface as &#x60;410 Gone&#x60; with a distinct &#x60;code&#x60; (&#x60;link_deleted&#x60; vs &#x60;link_expired&#x60;) so programmatic clients can distinguish a once-valid code from one that never existed (&#x60;404&#x60;). 

### Example

```ts
import {
  Configuration,
  LinksApi,
} from '';
import type { GetLinkRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new LinksApi();

  const body = {
    // string | The short code identifying the link. Must match `^[0-9A-Za-z]{4,64}$`; codes that fail this shape check are rejected with `404` rather than `422` so the API doesn\'t leak its validation rules through the path parameter. 
    code: abc1234,
  } satisfies GetLinkRequest;

  try {
    const data = await api.getLink(body);
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

[**LinkResponse**](LinkResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | The link metadata. |  -  |
| **404** | The code does not exist (or is malformed). |  -  |
| **410** | The link existed but is no longer active. Inspect the &#x60;code&#x60; field to distinguish soft-delete (&#x60;link_deleted&#x60;) from expiry (&#x60;link_expired&#x60;).  |  -  |
| **500** | Server-side failure; details are logged on the server, not surfaced. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)


## listLinks

> ListResponse listLinks(limit, before)

List recent links.

Returns a page of links ordered newest-first (by internal id), suitable for the recent-links view in the web UI. Pagination is cursor-based: pass the previous response\&#39;s &#x60;next_cursor&#x60; as the &#x60;before&#x60; query parameter on the next request to walk older rows. &#x60;next_cursor&#x60; is &#x60;null&#x60; on the last page.  Soft-deleted and expired rows are excluded server-side, so a busy site that prunes regularly still pages predictably. Bad or out-of-range &#x60;limit&#x60; values fall back to the default rather than failing the request. 

### Example

```ts
import {
  Configuration,
  LinksApi,
} from '';
import type { ListLinksRequest } from '';

async function example() {
  console.log("🚀 Testing  SDK...");
  const api = new LinksApi();

  const body = {
    // number | Page size. Defaults to 10. Values above 100 are silently clamped down. Non-positive or non-numeric values fall back to the default.  (optional)
    limit: 56,
    // number | Exclusive lower bound on the row id; pass the previous page\'s `next_cursor` to walk backwards in time. Omit (or pass `0`) for the first page.  (optional)
    before: 789,
  } satisfies ListLinksRequest;

  try {
    const data = await api.listLinks(body);
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
| **limit** | `number` | Page size. Defaults to 10. Values above 100 are silently clamped down. Non-positive or non-numeric values fall back to the default.  | [Optional] [Defaults to `10`] |
| **before** | `number` | Exclusive lower bound on the row id; pass the previous page\&#39;s &#x60;next_cursor&#x60; to walk backwards in time. Omit (or pass &#x60;0&#x60;) for the first page.  | [Optional] [Defaults to `undefined`] |

### Return type

[**ListResponse**](ListResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: `application/json`


### HTTP response details
| Status code | Description | Response headers |
|-------------|-------------|------------------|
| **200** | A page of links. |  -  |
| **500** | Server-side failure; details are logged on the server, not surfaced. |  -  |

[[Back to top]](#) [[Back to API list]](../README.md#api-endpoints) [[Back to Model list]](../README.md#models) [[Back to README]](../README.md)

