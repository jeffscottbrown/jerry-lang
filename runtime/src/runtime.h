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

/* JerryArray: dynamic array (generic, element size tracked at runtime).
   heap_elems=1 means each element slot holds a heap pointer (JerryStr*,
   class instance, etc.) that participates in reference counting: push/set
   retain the incoming pointer, the destructor releases all stored pointers,
   and set releases the displaced pointer. */
typedef struct {
    int64_t len;
    int64_t cap;
    char*   data;
    int64_t elem_size;
    int8_t  heap_elems;
} JerryArray;

/* JerryClosure: first-class function value */
typedef struct {
    void (*fn_ptr)(void* env, JerryStr* str); // Typed function pointer
    void* env_ptr;
} JerryClosure;

/* ── Memory and reference counting ──────────────────────────────────────────── */
/* Every jerry_alloc allocation is preceded by a 16-byte JerryHeader (stored
   internally in runtime.c).  External code never touches the header directly;
   use jerry_retain / jerry_release to manage object lifetimes.

   Ownership rules:
   - jerry_alloc returns a pointer with refcount = 1.  The caller owns it.
   - Passing a value to a function is a *borrow* — callee must not release it
     unless it also called jerry_retain first.
   - Functions that return newly allocated objects (jerry_string_new, new-expr,
     etc.) return a +1 retained reference; the caller owns it.            */
void* jerry_alloc(int64_t size);
void  jerry_retain(void* ptr);   /* increment refcount (no-op on NULL)         */
void  jerry_release(void* ptr);  /* decrement refcount; free when it reaches 0 */

/* ── Strings ────────────────────────────────────────────────────────────────── */
JerryStr* jerry_string_new(const char* data, int64_t len);
JerryStr* jerry_string_concat(JerryStr* a, JerryStr* b);
int8_t     jerry_string_eq(JerryStr* a, JerryStr* b);
int8_t     jerry_string_ne(JerryStr* a, JerryStr* b);
int64_t    jerry_string_len(JerryStr* s);
int64_t    jerry_char_at(JerryStr* s, int64_t i);        /* char code at index   */
JerryStr*  jerry_string_slice(JerryStr* s, int64_t start, int64_t end); /* s[start:end] */
JerryStr*  jerry_char_to_string(int64_t code);            /* char code → 1-char string */
int8_t     jerry_string_contains(JerryStr* s, JerryStr* sub);
int8_t     jerry_string_starts_with(JerryStr* s, JerryStr* prefix);
int8_t     jerry_string_ends_with(JerryStr* s, JerryStr* suffix);
int64_t    jerry_string_index_of(JerryStr* s, JerryStr* sub);
int64_t    jerry_string_to_int(JerryStr* s);
JerryStr*  jerry_read_bytes(int64_t n);
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
JerryStr*  jerry_getenv(JerryStr* name);
void       jerry_delete_file(JerryStr* path);
int8_t     jerry_is_dir(JerryStr* path);
JerryArray* jerry_list_dir(JerryStr* path);
JerryStr*  jerry_runtime_lib_path(void);
JerryStr*  jerry_stdlib_dir_path(void);
int64_t    jerry_exec(JerryArray* args);

/* ── Arrays ─────────────────────────────────────────────────────────────────── */
JerryArray* jerry_array_new(int64_t elem_size, int64_t initial_cap, int8_t heap_elems);
void         jerry_array_mark_heap(JerryArray* arr);
void*        jerry_array_get(JerryArray* arr, int64_t idx);
void         jerry_array_set(JerryArray* arr, int64_t idx, void* elem);
int64_t      jerry_array_len(JerryArray* arr);
void         jerry_array_push(JerryArray* arr, void* elem);

/* ── Maps ────────────────────────────────────────────────────────────────────── */
/* JerryMap: hash map with string or int64 keys, fixed-size values.
   string_keys=1 → keys are JerryStr* compared by content.
   string_keys=0 → keys are int64 compared by value.                            */
typedef struct JerryMapNode {
    uint8_t              key[8];   /* raw key bytes (JerryStr* or int64)         */
    uint8_t*             value;    /* malloc'd copy of value bytes               */
    struct JerryMapNode* next;
} JerryMapNode;

typedef struct {
    JerryMapNode** buckets;
    int64_t        bucket_count;
    int64_t        len;
    int64_t        value_size;
    int8_t         string_keys;
} JerryMap;

JerryMap*   jerry_map_new(int8_t string_keys, int64_t value_size);
void        jerry_map_set(JerryMap* m, void* key, void* value);
void*       jerry_map_get(JerryMap* m, void* key);   /* panics if key absent     */
int8_t      jerry_map_has(JerryMap* m, void* key);
void        jerry_map_delete(JerryMap* m, void* key);
int64_t     jerry_map_len(JerryMap* m);
JerryArray* jerry_map_keys(JerryMap* m);             /* returns array of keys    */

/* ── Control ────────────────────────────────────────────────────────────────── */
void jerry_panic(JerryStr* msg) __attribute__((noreturn));
void jerry_exit(int64_t code)   __attribute__((noreturn));

/* ── Program arguments ──────────────────────────────────────────────────────── */
void        jerry_capture_args(int64_t argc, char** argv);
JerryArray* jerry_args(void);

/* ── I/O extras ─────────────────────────────────────────────────────────────── */
void      jerry_print_err(JerryStr* s);
JerryStr* jerry_read_stdin(void);

/* ── Time ────────────────────────────────────────────────────────────────────────────────── */
int64_t    jerry_now_millis(void);   /* Unix epoch in milliseconds              */
int64_t    jerry_now_seconds(void);  /* Unix epoch in seconds                   */
JerryStr*  jerry_now_string(void);   /* Local time as "YYYY-MM-DD HH:MM:SS"    */

#endif /* JERRY_RUNTIME_H */
