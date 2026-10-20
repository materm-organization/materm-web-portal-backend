package convertor

import (
	"fmt"
	"strconv"
)

// ==================== UINT Conversions ====================

// UintToString converts uint to string
func UintToString(val uint) string {
	return strconv.FormatUint(uint64(val), 10)
}

// StringToUint converts string to uint
// Returns 0 and error if conversion fails
func StringToUint(s string) (uint, error) {
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to convert string to uint: %w", err)
	}
	return uint(val), nil
}

// StringToUintSafe converts string to uint, returns 0 if conversion fails
func StringToUintSafe(s string) uint {
	val, err := StringToUint(s)
	if err != nil {
		fmt.Printf("failed to convert string to uint: %v", err)
	}
	return val
}

// ==================== INT Conversions ====================

// IntToString converts int to string
func IntToString(val int) string {
	return strconv.Itoa(val)
}

// StringToInt converts string to int
// Returns 0 and error if conversion fails
func StringToInt(s string) (int, error) {
	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("failed to convert string to int: %w", err)
	}
	return val, nil
}

// StringToIntSafe converts string to int, returns 0 if conversion fails
func StringToIntSafe(s string) int {
	val, _ := StringToInt(s)
	return val
}

// Int64ToString converts int64 to string
func Int64ToString(val int64) string {
	return strconv.FormatInt(val, 10)
}

// StringToInt64 converts string to int64
// Returns 0 and error if conversion fails
func StringToInt64(s string) (int64, error) {
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to convert string to int64: %w", err)
	}
	return val, nil
}

// StringToInt64Safe converts string to int64, returns 0 if conversion fails
func StringToInt64Safe(s string) int64 {
	val, _ := StringToInt64(s)
	return val
}

// ==================== FLOAT Conversions ====================

// Float32ToString converts float32 to string
// precision: number of decimal places (default: -1 for full precision)
func Float32ToString(val float32, precision int) string {
	if precision < 0 {
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	}
	return strconv.FormatFloat(float64(val), 'f', precision, 32)
}

// StringToFloat32 converts string to float32
// Returns 0 and error if conversion fails
func StringToFloat32(s string) (float32, error) {
	val, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, fmt.Errorf("failed to convert string to float32: %w", err)
	}
	return float32(val), nil
}

// StringToFloat32Safe converts string to float32, returns 0 if conversion fails
func StringToFloat32Safe(s string) float32 {
	val, _ := StringToFloat32(s)
	return val
}

// Float64ToString converts float64 to string
// precision: number of decimal places (default: -1 for full precision)
func Float64ToString(val float64, precision int) string {
	if precision < 0 {
		return strconv.FormatFloat(val, 'f', -1, 64)
	}
	return strconv.FormatFloat(val, 'f', precision, 64)
}

// StringToFloat64 converts string to float64
// Returns 0 and error if conversion fails
func StringToFloat64(s string) (float64, error) {
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to convert string to float64: %w", err)
	}
	return val, nil
}

// StringToFloat64Safe converts string to float64, returns 0 if conversion fails
func StringToFloat64Safe(s string) float64 {
	val, _ := StringToFloat64(s)
	return val
}

// ==================== Generic Float Conversions (Simplified) ====================

// FloatToString converts float64 to string with default precision (2 decimal places)
func FloatToString(val float64) string {
	return Float64ToString(val, 2)
}

// StringToFloat converts string to float64
// Returns 0 and error if conversion fails
func StringToFloat(s string) (float64, error) {
	return StringToFloat64(s)
}

// StringToFloatSafe converts string to float64, returns 0 if conversion fails
func StringToFloatSafe(s string) float64 {
	return StringToFloat64Safe(s)
}
