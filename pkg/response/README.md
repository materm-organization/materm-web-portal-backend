# Response Utility

Standard response helper for consistent API responses across all endpoints.

## Why Use This?

✅ **Consistent format** - All responses follow the same structure
✅ **Clean code** - No more `fiber.Map{}` everywhere
✅ **Type-safe** - Proper response structure
✅ **Easy to use** - Simple helper functions
✅ **Better documentation** - Clear response patterns

## Standard Response Format

All API responses follow this structure:

```json
{
  "success": true,
  "message": "Optional message",
  "data": { ... },
  "error": "Error message if failed",
  "details": "Additional error details if needed"
}
```

## Usage

### Success Responses

```go
import "yourproject/pkg/response"

// 200 OK - Get/List
func (h *Handler) GetByID(c *fiber.Ctx) error {
    result, err := h.service.GetByID(id)
    if err != nil {
        return response.NotFound(c, "User not found")
    }
    return response.OK(c, result)
}
// Response: {"success": true, "data": {...}}

// 201 Created - Create
func (h *Handler) Create(c *fiber.Ctx) error {
    result, err := h.service.Create(&req)
    if err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }
    return response.Created(c, "User created successfully", result)
}
// Response: {"success": true, "message": "User created successfully", "data": {...}}

// 204 No Content - Delete
func (h *Handler) Delete(c *fiber.Ctx) error {
    if err := h.service.Delete(id); err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }
    return response.NoContent(c)
}
// Response: No content, just HTTP 204 status
```

### Error Responses

```go
// 400 Bad Request
func (h *Handler) Create(c *fiber.Ctx) error {
    var req CreateRequest
    if err := c.BodyParser(&req); err != nil {
        return response.BadRequest(c, "Invalid request body", nil)
    }
    // ...
}
// Response: {"success": false, "error": "Invalid request body"}

// 400 Bad Request with validation details
func (h *Handler) Create(c *fiber.Ctx) error {
    if err := validate.Struct(&req); err != nil {
        return response.ValidationError(c, err.Error())
    }
    // ...
}
// Response: {"success": false, "error": "Validation failed", "details": "..."}

// 401 Unauthorized
func (h *Handler) Protected(c *fiber.Ctx) error {
    if !isAuthenticated {
        return response.Unauthorized(c, "Authentication required")
    }
    // ...
}
// Response: {"success": false, "error": "Authentication required"}

// 403 Forbidden
func (h *Handler) AdminOnly(c *fiber.Ctx) error {
    if !isAdmin {
        return response.Forbidden(c, "Admin access required")
    }
    // ...
}
// Response: {"success": false, "error": "Admin access required"}

// 404 Not Found
func (h *Handler) GetByID(c *fiber.Ctx) error {
    result, err := h.service.GetByID(id)
    if err != nil {
        return response.NotFound(c, "User not found")
    }
    return response.OK(c, result)
}
// Response: {"success": false, "error": "User not found"}

// 409 Conflict
func (h *Handler) Create(c *fiber.Ctx) error {
    if exists {
        return response.Conflict(c, "User already exists")
    }
    // ...
}
// Response: {"success": false, "error": "User already exists"}

// 500 Internal Server Error
func (h *Handler) Create(c *fiber.Ctx) error {
    result, err := h.service.Create(&req)
    if err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }
    return response.Created(c, "User created", result)
}
// Response: {"success": false, "error": "Database connection failed"}
```

## Available Functions

### Success Functions

| Function | Status | Use Case |
|----------|--------|----------|
| `OK(c, data)` | 200 | GET requests, successful operations |
| `Created(c, message, data)` | 201 | POST requests, resource created |
| `NoContent(c)` | 204 | DELETE requests, successful with no content |
| `Success(c, code, message, data)` | Custom | Custom success responses |
| `SuccessWithData(c, code, data)` | Custom | Custom success without message |

### Error Functions

| Function | Status | Use Case |
|----------|--------|----------|
| `BadRequest(c, message, details)` | 400 | Invalid input, parse errors |
| `ValidationError(c, details)` | 400 | Validation failures |
| `Unauthorized(c, message)` | 401 | Authentication required |
| `Forbidden(c, message)` | 403 | Insufficient permissions |
| `NotFound(c, message)` | 404 | Resource not found |
| `Conflict(c, message)` | 409 | Resource conflict |
| `InternalServerError(c, message, details)` | 500 | Server errors |
| `Error(c, code, message, details)` | Custom | Custom error responses |

## Complete Example

```go
package user

import (
    "yourproject/pkg/response"
    "github.com/go-playground/validator/v10"
    "github.com/gofiber/fiber/v2"
)

var validate = validator.New()

type Handler struct {
    service *Service
}

func (h *Handler) Create(c *fiber.Ctx) error {
    var req CreateRequest

    // Parse request
    if err := c.BodyParser(&req); err != nil {
        return response.BadRequest(c, "Invalid request body", nil)
    }

    // Validate
    if err := validate.Struct(&req); err != nil {
        return response.ValidationError(c, err.Error())
    }

    // Business logic
    result, err := h.service.Create(&req)
    if err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }

    return response.Created(c, "User created successfully", result)
}

func (h *Handler) GetByID(c *fiber.Ctx) error {
    id, err := c.ParamsInt("id")
    if err != nil {
        return response.BadRequest(c, "Invalid ID", nil)
    }

    result, err := h.service.GetByID(uint(id))
    if err != nil {
        return response.NotFound(c, "User not found")
    }

    return response.OK(c, result)
}

func (h *Handler) List(c *fiber.Ctx) error {
    results, err := h.service.List()
    if err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }

    return response.OK(c, ListResponse{Data: results})
}

func (h *Handler) Update(c *fiber.Ctx) error {
    id, err := c.ParamsInt("id")
    if err != nil {
        return response.BadRequest(c, "Invalid ID", nil)
    }

    var req UpdateRequest
    if err := c.BodyParser(&req); err != nil {
        return response.BadRequest(c, "Invalid request body", nil)
    }

    if err := validate.Struct(&req); err != nil {
        return response.ValidationError(c, err.Error())
    }

    result, err := h.service.Update(uint(id), &req)
    if err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }

    return response.OK(c, result)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
    id, err := c.ParamsInt("id")
    if err != nil {
        return response.BadRequest(c, "Invalid ID", nil)
    }

    if err := h.service.Delete(uint(id)); err != nil {
        return response.InternalServerError(c, err.Error(), nil)
    }

    return response.NoContent(c)
}
```

## Response Examples

### Success Response (200 OK)
```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "John Doe",
    "email": "john@example.com",
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

### Created Response (201 Created)
```json
{
  "success": true,
  "message": "User created successfully",
  "data": {
    "id": 1,
    "name": "John Doe",
    "email": "john@example.com"
  }
}
```

### Error Response (400 Bad Request)
```json
{
  "success": false,
  "error": "Validation failed",
  "details": "Name: min length is 3, Email: must be a valid email"
}
```

### Error Response (404 Not Found)
```json
{
  "success": false,
  "error": "User not found"
}
```

## Best Practices

1. **Always use response helpers** - Never use `fiber.Map{}` directly
2. **Clear error messages** - Use descriptive error messages
3. **Validation errors** - Use `ValidationError()` for validation
4. **Business errors** - Use appropriate status codes
5. **Consistent patterns** - Follow the same patterns across all handlers
6. **No details for 401/403** - Don't expose security details
7. **Include details for 400** - Help users fix validation errors

## Migration from fiber.Map

### Before (Don't do this)
```go
return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
    "error": "Invalid request body",
})
```

### After (Do this)
```go
return response.BadRequest(c, "Invalid request body", nil)
```

---

**All handlers/controllers should use the response utility for consistent API responses!**
