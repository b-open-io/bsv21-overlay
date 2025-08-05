# BSV21 Overlay API Reference

## Overview

The BSV21 overlay service provides HTTP endpoints for querying token events, calculating balances, and subscribing to real-time updates.

## Base URL

```
http://localhost:3000
```

## Authentication

Currently, the API does not require authentication. In production, you should implement appropriate security measures.

## Endpoints

### Query Events

#### GET /1sat/events/:event

Retrieve events of a specific type with pagination support.

**Parameters:**

| Name | Type | Location | Description | Required |
|------|------|----------|-------------|----------|
| event | string | path | Event type to query | Yes |
| from | string | query | Starting position as `height.idx` | No |
| limit | integer | query | Number of results (max 1000) | No |

**Event Types:**
- `id:{tokenId}` - All events for a token
- `sym:{symbol}` - Events by token symbol
- `p2pkh:{address}:{tokenId}` - P2PKH outputs for address and token
- `cos:{address}:{tokenId}` - Cosigner outputs
- `ltm:{tokenId}` - Lock-to-mint events
- `pow20:{tokenId}` - Proof-of-work events
- `list:{seller}:{tokenId}` - Marketplace listings by seller
- `list:{tokenId}` - All listings for a token

**Example Request:**
```bash
curl -X GET "http://localhost:3000/1sat/events/id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1?from=850000.0&limit=50"
```

**Example Response:**
```json
{
  "type": "output-list",
  "outputs": [
    {
      "outputIndex": 0,
      "beef": "0x..."
    },
    {
      "outputIndex": 1,
      "beef": "0x..."
    }
  ]
}
```

**Status Codes:**
- `200 OK` - Success
- `400 Bad Request` - Invalid parameters
- `500 Internal Server Error` - Server error

---

### Get Token Balance

#### GET /1sat/bsv21/:event/balance

Calculate the total balance for unspent outputs of an event type.

**Parameters:**

| Name | Type | Location | Description | Required |
|------|------|----------|-------------|----------|
| event | string | path | Event type to calculate balance for | Yes |

**Example Request:**
```bash
curl -X GET "http://localhost:3000/1sat/bsv21/id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1/balance"
```

**Example Response:**
```json
{
  "event": "id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1",
  "balance": 1000000,
  "outputs": 25
}
```

**Notes:**
- Balance is returned as uint64
- Only unspent outputs are included
- The balance represents the sum of all token amounts

**Status Codes:**
- `200 OK` - Success
- `500 Internal Server Error` - Calculation error

---

### Subscribe to Events

#### GET /subscribe/:topics

Subscribe to real-time events using Server-Sent Events (SSE).

**Parameters:**

| Name | Type | Location | Description | Required |
|------|------|----------|-------------|----------|
| topics | string | path | Comma-separated list of topics | Yes |

**Example Request:**
```javascript
const eventSource = new EventSource('http://localhost:3000/subscribe/tm_tokenId1,tm_tokenId2');

eventSource.onmessage = (event) => {
    console.log('Event ID:', event.lastEventId);
    console.log('Event Data:', event.data);
};

eventSource.onerror = (error) => {
    console.error('SSE Error:', error);
};
```

**Event Format:**
```
id: 850000_125
data: {"type":"transfer","tokenId":"...","amount":1000,"from":"...","to":"..."}

id: 850000_126
data: {"type":"mint","tokenId":"...","amount":5000,"to":"..."}
```

**Connection Management:**
- Clients should handle reconnection on connection loss
- The `id` field allows resuming from a specific point
- Keep-alive messages are sent periodically

---

### Submit Transaction

#### POST /submit

Submit a tagged BEEF transaction for processing.

**Headers:**

| Name | Value | Description | Required |
|------|-------|-------------|----------|
| Content-Type | application/octet-stream | Binary content type | Yes |
| x-topics | string | Comma-separated topic list | Yes |

**Request Body:**
Raw BEEF (Binary Extended Format) bytes

**Example Request:**
```bash
curl -X POST "http://localhost:3000/submit" \
  -H "Content-Type: application/octet-stream" \
  -H "x-topics: tm_token1,tm_token2" \
  --data-binary @transaction.beef
```

**Example Response:**
```json
{
  "status": "accepted",
  "txid": "..."
}
```

**Status Codes:**
- `200 OK` - Transaction accepted
- `400 Bad Request` - Invalid BEEF or missing headers
- `500 Internal Server Error` - Processing error

---

### Lookup Service

#### POST /lookup

Query events using the generic lookup service protocol.

**Headers:**

| Name | Value | Description | Required |
|------|-------|-------------|----------|
| Content-Type | application/json | JSON content type | Yes |

**Request Body:**
```json
{
  "service": "ls_bsv21",
  "query": {
    "event": "id:tokenId",
    "from": {
      "height": 850000,
      "idx": 0
    },
    "limit": 100,
    "spent": false,
    "reverse": false
  }
}
```

**Query Parameters:**
- `event` - Single event type to query
- `events` - Array of event types (for multi-event queries)
- `from` - Starting position with height and index
- `limit` - Maximum results to return
- `spent` - Filter by spent status (true/false/null for all)
- `reverse` - Reverse chronological order

**Example Response:**
```json
{
  "type": "output-list",
  "outputs": [
    {
      "outputIndex": 0,
      "beef": "0x..."
    }
  ]
}
```

---

### ARC Webhook

#### POST /arc-ingest

Webhook endpoint for ARC transaction status updates.

**Headers:**

| Name | Value | Description | Required |
|------|-------|-------------|----------|
| Authorization | Bearer {token} | ARC callback token | Yes |

**Request Body:**
ARC transaction status payload (format depends on ARC configuration)

**Status Codes:**
- `200 OK` - Update processed
- `401 Unauthorized` - Invalid or missing token
- `500 Internal Server Error` - Processing error

## Error Responses

All endpoints return errors in a consistent format:

```json
{
  "error": "Error message description"
}
```

## Rate Limiting

Currently no rate limiting is implemented. In production, consider adding rate limits to prevent abuse.

## Pagination

For endpoints that support pagination (`/1sat/events/:event`):

1. Start with no `from` parameter to get the earliest events
2. Use the last event's position from the response as the `from` parameter for the next page
3. Continue until you receive fewer results than your `limit`

Example pagination flow:
```bash
# First page
GET /1sat/events/id:token?limit=100

# Next page (if last event was at height 850123, idx 45)
GET /1sat/events/id:token?from=850123.45&limit=100
```

## WebSocket Support

Currently not implemented. All real-time updates use Server-Sent Events (SSE).

## CORS

CORS headers are set for the SSE endpoint:
- `Access-Control-Allow-Origin: *`

For production, configure appropriate CORS policies.

## Best Practices

1. **Error Handling**: Always check status codes and handle errors appropriately
2. **Pagination**: Use pagination for large result sets to avoid timeouts
3. **SSE Reconnection**: Implement automatic reconnection for SSE clients
4. **Binary Data**: When submitting BEEF transactions, ensure proper binary encoding
5. **Event Types**: Use the most specific event type for better performance

## Examples

### Get all tokens for an address
```bash
# This would require multiple queries since we store p2pkh:address:tokenId
# You'd need to know which tokens to query for
curl "http://localhost:3000/1sat/events/p2pkh:1Address:token1/balance"
curl "http://localhost:3000/1sat/events/p2pkh:1Address:token2/balance"
```

### Monitor token transfers in real-time
```javascript
const tokenId = "36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1";
const eventSource = new EventSource(`http://localhost:3000/subscribe/tm_${tokenId}`);

eventSource.onmessage = (event) => {
    const data = JSON.parse(event.data);
    console.log('Token transfer:', data);
};
```

### Calculate total token supply
```bash
# Get balance for the token ID itself (all unspent outputs)
curl "http://localhost:3000/1sat/bsv21/id:tokenId/balance"
```