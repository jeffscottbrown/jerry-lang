#ifndef JERRY_RUNTIME_H
#define JERRY_RUNTIME_H

#include <stdint.h>
#include <stddef.h>

/* ── Core object types ──────────────────────────────────────────────────────── */

/* JerryStr: immutable string (heap-allocated data, null-terminated for C compat) */
typedef struct {
    int64_t len;
    char*   data;
} JerryStr;

/* JerryArray: dynamic array (generic, element size tracked at runtime) */
typedef struct {
    int64_t len;
    int64_t cap;
    char*   data;
    int64_t elem_size;
} JerryArray;

/* JerryClosure: first-class function value */
typedef struct {
    void (*fn_ptr)(void* env, JerryStr* str); // Typed function pointer
    void* env_ptr;
} JerryClosure;

/* ── Memory ─────────────────────────────────────────────────────────────────── */
/* Simple malloc wrapper — no GC in this version. Allocations are never freed.
   This is fine for a compiler (short-lived process) and simplifies the
   implementation. A mark-and-sweep GC is a good follow-up project to write
   in Jerry once the language is capable enough.                              */
void* jerry_alloc(int64_t size);

/* ── Strings ────────────────────────────────────────────────────────────────── */
JerryStr* jerry_string_new(const char* data, int64_t len);
JerryStr* jerry_string_concat(JerryStr* a, JerryStr* b);
int8_t     jerry_string_eq(JerryStr* a, JerryStr* b);
int8_t     jerry_string_ne(JerryStr* a, JerryStr* b);
int64_t    jerry_string_len(JerryStr* s);
int64_t    jerry_char_at(JerryStr* s, int64_t i);        /* char code at index   */
JerryStr*  jerry_string_slice(JerryStr* s, int64_t start, int64_t end); /* s[start:end] */
JerryStr*  jerry_char_to_string(int64_t code);            /* char code → 1-char string */
JerryStr* jerry_int_to_string(int64_t n);
JerryStr* jerry_float_to_string(double f);

/* ── I/O ────────────────────────────────────────────────────────────────────── */
void jerry_print_int(int64_t n);
void jerry_print_float(double f);
void jerry_print_bool(int8_t b);
void jerry_print_string(JerryStr* s);
void jerry_print_array(JerryArray* arr);
void jerry_println(void);
JerryStr* jerry_read_file(JerryStr* path);
void       jerry_write_file(JerryStr* path, JerryStr* content);

/* ── Arrays ─────────────────────────────────────────────────────────────────── */
JerryArray* jerry_array_new(int64_t elem_size, int64_t initial_cap);
void*        jerry_array_get(JerryArray* arr, int64_t idx);
void         jerry_array_set(JerryArray* arr, int64_t idx, void* elem);
int64_t      jerry_array_len(JerryArray* arr);
void         jerry_array_push(JerryArray* arr, void* elem);

/* ── Control ────────────────────────────────────────────────────────────────── */
void jerry_panic(JerryStr* msg) __attribute__((noreturn));
void jerry_exit(int64_t code)   __attribute__((noreturn));

/* ── Program arguments ──────────────────────────────────────────────────────── */
void        jerry_capture_args(int64_t argc, char** argv);
JerryArray* jerry_args(void);

/* ── I/O extras ─────────────────────────────────────────────────────────────── */
void      jerry_print_err(JerryStr* s);
JerryStr* jerry_read_stdin(void);

#endif /* JERRY_RUNTIME_H */
