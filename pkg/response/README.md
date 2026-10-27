# pkg/response

Success envelopes of the API (identical in the admin and the portal backend) (doc/05 section 1.3). Errors are not written here: a handler returns an
`*apperror.Error` (or any error) and `apperror.Handler`, the Fiber ErrorHandler, writes the error envelope.

```go
return response.OK(c, dto)                                   // {"success":true,"data":{...}}
return response.OKMessage(c, "saved", nil)                   // {"success":true,"message":"saved","data":null}
return response.Created(c, "/api/v1/things/7", dto)         // 201 + Location
return response.List(c, items, pagination.NewMeta(total, q)) // {"success":true,"data":[...],"meta":{...}}
return apperror.NotFound()                                   // {"success":false,"message":"...","code":"NOT_FOUND",...}
```

Rules: `snake_case` field names, no 204 (answer 200 with `data: null`), empty lists are `[]`, `meta` sits next to
`data` with `total_page` (not `totalPages`).
