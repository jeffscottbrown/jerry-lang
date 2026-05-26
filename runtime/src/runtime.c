#include "runtime.h"
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
#if defined(__APPLE__)
#  include <mach-o/dyld.h>   /* _NSGetExecutablePath */
#endif

/* ── Reference-counted allocator ────────────────────────────────────────────── */
/* Every allocation is preceded by a 16-byte header:
     [0..7]  int64_t  refcount    (starts at 1)
     [8..15] void*    destructor  (called just before the object is freed; may be NULL)
   jerry_alloc() returns a pointer 16 bytes past the start of the malloc'd block,
   so callers see only the object bytes.                                          */

typedef struct {
    int64_t  refcount;
    void   (*destructor)(void*);
} JerryHeader;

void* jerry_alloc(int64_t size) {
    JerryHeader* h = malloc(sizeof(JerryHeader) + (size_t)size);
    if (!h) {
        fprintf(stderr, "jerry: out of memory\n");
        exit(1);
    }
    memset(h, 0, sizeof(JerryHeader) + (size_t)size);
    h->refcount = 1;
    return h + 1; /* object starts immediately after the header */
}

void jerry_retain(void* ptr) {
    if (!ptr) return;
    JerryHeader* h = (JerryHeader*)ptr - 1;
    h->refcount++;
}

void jerry_release(void* ptr) {
    if (!ptr) return;
    JerryHeader* h = (JerryHeader*)ptr - 1;
    h->refcount--;
    if (h->refcount <= 0) {
        if (h->destructor) h->destructor(ptr);
        free(h);
    }
}

/* ── String destructor ───────────────────────────────────────────────────────── */

static void jerry_str_destructor(void* self) {
    JerryStr* s = (JerryStr*)self;
    free(s->data); /* data is a plain malloc'd buffer, not jerry_alloc'd */
}

/* Helper: allocate a JerryStr with its destructor installed. */
static JerryStr* alloc_str(int64_t len) {
    JerryStr* s = jerry_alloc(sizeof(JerryStr));
    JerryHeader* h = (JerryHeader*)s - 1;
    h->destructor = jerry_str_destructor;
    s->len  = len;
    s->data = malloc((size_t)(len + 1));
    if (!s->data) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    s->data[len] = '\0';
    return s;
}

/* ── Array destructor ────────────────────────────────────────────────────────── */

static void jerry_arr_destructor(void* self) {
    JerryArray* arr = (JerryArray*)self;
    if (arr->heap_elems) {
        for (int64_t i = 0; i < arr->len; i++) {
            void* ptr;
            memcpy(&ptr, arr->data + i * arr->elem_size, sizeof(void*));
            jerry_release(ptr);
        }
    }
    free(arr->data); /* data is a plain malloc'd buffer */
}

/* ── Strings ────────────────────────────────────────────────────────────────── */

JerryStr* jerry_string_new(const char* data, int64_t len) {
    JerryStr* s = alloc_str(len);
    if (len > 0 && data != NULL) {
        memcpy(s->data, data, (size_t)len);
    }
    return s;
}

JerryStr* jerry_string_concat(JerryStr* a, JerryStr* b) {
    int64_t len = a->len + b->len;
    JerryStr* s = alloc_str(len);
    memcpy(s->data,          a->data, (size_t)a->len);
    memcpy(s->data + a->len, b->data, (size_t)b->len);
    return s;
}

int8_t jerry_string_eq(JerryStr* a, JerryStr* b) {
    if (a == b) return 1;
    if (!a || !b) return 0;
    if (a->len != b->len) return 0;
    return (int8_t)(memcmp(a->data, b->data, (size_t)a->len) == 0);
}

int8_t jerry_string_ne(JerryStr* a, JerryStr* b) {
    return (int8_t)(!jerry_string_eq(a, b));
}

int64_t jerry_string_cmp(JerryStr* a, JerryStr* b) {
    if (a == b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    int64_t min_len = a->len < b->len ? a->len : b->len;
    int r = memcmp(a->data, b->data, (size_t)min_len);
    if (r != 0) return (int64_t)r;
    return a->len - b->len;
}

int64_t jerry_string_len(JerryStr* s) {
    return s->len;
}

int64_t jerry_char_at(JerryStr* s, int64_t i) {
    if (i < 0 || i >= s->len) {
        fprintf(stderr, "jerry: char_at: index %lld out of bounds (len %lld)\n",
                (long long)i, (long long)s->len);
        exit(1);
    }
    return (int64_t)(unsigned char)s->data[i];
}

JerryStr* jerry_string_slice(JerryStr* s, int64_t start, int64_t end) {
    if (start < 0) start = 0;
    if (end > s->len) end = s->len;
    if (start > end) start = end;
    return jerry_string_new(s->data + start, end - start);
}

JerryStr* jerry_char_to_string(int64_t code) {
    char buf[1];
    buf[0] = (char)(code & 0xFF);
    return jerry_string_new(buf, 1);
}

int8_t jerry_string_contains(JerryStr* s, JerryStr* sub) {
    if (s == NULL || sub == NULL) return 0;
    if (sub->len == 0) return 1;
    if (sub->len > s->len) return 0;
    for (int64_t i = 0; i <= s->len - sub->len; i++) {
        if (memcmp(s->data + i, sub->data, (size_t)sub->len) == 0) return 1;
    }
    return 0;
}

int8_t jerry_string_contains_range(JerryStr* s, int64_t start, int64_t end, JerryStr* sub) {
    if (s == NULL || sub == NULL) return 0;
    if (start < 0) start = 0;
    if (end > s->len) end = s->len;
    int64_t rlen = end - start;
    if (sub->len == 0) return 1;
    if (sub->len > rlen) return 0;
    for (int64_t i = start; i <= start + rlen - sub->len; i++) {
        if (memcmp(s->data + i, sub->data, (size_t)sub->len) == 0) return 1;
    }
    return 0;
}

int8_t jerry_string_starts_with(JerryStr* s, JerryStr* prefix) {
    if (s == NULL || prefix == NULL) return 0;
    if (prefix->len == 0) return 1;
    if (prefix->len > s->len) return 0;
    return (int8_t)(memcmp(s->data, prefix->data, (size_t)prefix->len) == 0);
}

int8_t jerry_string_ends_with(JerryStr* s, JerryStr* suffix) {
    if (s == NULL || suffix == NULL) return 0;
    if (suffix->len == 0) return 1;
    if (suffix->len > s->len) return 0;
    return (int8_t)(memcmp(s->data + s->len - suffix->len, suffix->data, (size_t)suffix->len) == 0);
}

int64_t jerry_string_index_of(JerryStr* s, JerryStr* sub) {
    if (s == NULL || sub == NULL) return -1;
    if (sub->len == 0) return 0;
    if (sub->len > s->len) return -1;
    for (int64_t i = 0; i <= s->len - sub->len; i++) {
        if (memcmp(s->data + i, sub->data, (size_t)sub->len) == 0) return i;
    }
    return -1;
}

int64_t jerry_string_to_int(JerryStr* s) {
    if (s == NULL || s->len == 0) return 0;
    /* strtoll requires a null-terminated string */
    char buf[64];
    int64_t n = s->len < (int64_t)sizeof(buf) ? s->len : (int64_t)sizeof(buf) - 1;
    memcpy(buf, s->data, (size_t)n);
    buf[n] = '\0';
    return (int64_t)strtoll(buf, NULL, 10);
}

JerryStr* jerry_string_to_lower(JerryStr* s) {
    if (s == NULL) return jerry_string_new("", 0);
    char* buf = malloc((size_t)s->len);
    if (!buf) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    for (int64_t i = 0; i < s->len; i++)
        buf[i] = (char)tolower((unsigned char)s->data[i]);
    JerryStr* r = jerry_string_new(buf, s->len);
    free(buf);
    return r;
}

JerryStr* jerry_string_to_upper(JerryStr* s) {
    if (s == NULL) return jerry_string_new("", 0);
    char* buf = malloc((size_t)s->len);
    if (!buf) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    for (int64_t i = 0; i < s->len; i++)
        buf[i] = (char)toupper((unsigned char)s->data[i]);
    JerryStr* r = jerry_string_new(buf, s->len);
    free(buf);
    return r;
}

JerryStr* jerry_string_trim(JerryStr* s) {
    if (s == NULL) return jerry_string_new("", 0);
    int64_t lo = 0, hi = s->len;
    while (lo < hi && (unsigned char)s->data[lo] <= ' ') lo++;
    while (hi > lo && (unsigned char)s->data[hi - 1] <= ' ') hi--;
    return jerry_string_new(s->data + lo, hi - lo);
}

JerryStr* jerry_string_replace(JerryStr* s, JerryStr* from, JerryStr* to) {
    if (s == NULL) return jerry_string_new("", 0);
    if (from == NULL || from->len == 0) {
        jerry_retain(s);
        return s;
    }
    /* Count occurrences to preallocate result buffer. */
    int64_t count = 0;
    for (int64_t i = 0; i <= s->len - from->len; ) {
        if (memcmp(s->data + i, from->data, (size_t)from->len) == 0) {
            count++;
            i += from->len;
        } else {
            i++;
        }
    }
    int64_t new_len = s->len + count * (to == NULL ? 0 : to->len) - count * from->len;
    char* buf = malloc((size_t)(new_len > 0 ? new_len : 1));
    if (!buf) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    int64_t wi = 0, ri = 0;
    while (ri <= s->len - from->len) {
        if (memcmp(s->data + ri, from->data, (size_t)from->len) == 0) {
            if (to != NULL && to->len > 0) {
                memcpy(buf + wi, to->data, (size_t)to->len);
                wi += to->len;
            }
            ri += from->len;
        } else {
            buf[wi++] = s->data[ri++];
        }
    }
    while (ri < s->len) buf[wi++] = s->data[ri++];
    JerryStr* r = jerry_string_new(buf, wi);
    free(buf);
    return r;
}

JerryStr* jerry_read_bytes(int64_t n) {
    if (n <= 0) return jerry_string_new("", 0);
    char* buf = malloc((size_t)n);
    if (!buf) {
        fprintf(stderr, "jerry: read_bytes: out of memory\n");
        exit(1);
    }
    int64_t got = (int64_t)fread(buf, 1, (size_t)n, stdin);
    JerryStr* s = jerry_string_new(buf, got);
    free(buf);
    return s;
}

JerryStr* jerry_int_to_string(int64_t n) {
    char buf[32];
    int  len = snprintf(buf, sizeof(buf), "%lld", (long long)n);
    return jerry_string_new(buf, (int64_t)len);
}

JerryStr* jerry_float_to_string(double f) {
    char buf[64];
    int  len = snprintf(buf, sizeof(buf), "%g", f);
    return jerry_string_new(buf, (int64_t)len);
}

/* ── I/O ────────────────────────────────────────────────────────────────────── */

void jerry_print_int(int64_t n) {
    printf("%lld", (long long)n);
}

void jerry_print_float(double f) {
    /* Avoid "-0" and trailing zeros */
    printf("%g", f);
}

void jerry_print_bool(int8_t b) {
    fputs(b ? "true" : "false", stdout);
}

void jerry_print_string(JerryStr* s) {
    if (s == NULL) {
        fputs("null", stdout);
        return;
    }
    fwrite(s->data, 1, (size_t)s->len, stdout);
}

void jerry_print_string_range(JerryStr* s, int64_t start, int64_t end) {
    if (s == NULL) return;
    if (start < 0) start = 0;
    if (end > s->len) end = s->len;
    if (start < end) fwrite(s->data + start, 1, (size_t)(end - start), stdout);
    putchar('\n');
}

void jerry_print_array(JerryArray* arr) {
    if (arr == NULL) {
        fputs("null", stdout);
        return;
    }
    printf("[array len=%lld]", (long long)arr->len);
}

void jerry_println(void) {
    putchar('\n');
}

JerryStr* jerry_read_file(JerryStr* path) {
    if (path == NULL) {
        fprintf(stderr, "jerry: read_file: null path\n");
        exit(1);
    }
    FILE* f = fopen(path->data, "rb");
    if (!f) {
        fprintf(stderr, "jerry: read_file: cannot open '%s'\n", path->data);
        exit(1);
    }
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    JerryStr* s = alloc_str((int64_t)size);
    if (size > 0) {
        size_t n = fread(s->data, 1, (size_t)size, f);
        (void)n;
    }
    fclose(f);
    return s;
}

void jerry_write_file(JerryStr* path, JerryStr* content) {
    if (path == NULL || content == NULL) {
        fprintf(stderr, "jerry: write_file: null argument\n");
        exit(1);
    }
    FILE* f = fopen(path->data, "wb");
    if (!f) {
        fprintf(stderr, "jerry: write_file: cannot open '%s' for writing\n", path->data);
        exit(1);
    }
    fwrite(content->data, 1, (size_t)content->len, f);
    fclose(f);
}

void jerry_delete_file(JerryStr* path) {
    if (path == NULL) return;
    remove(path->data);
}

int8_t jerry_is_dir(JerryStr* path) {
    if (path == NULL) return 0;
    struct stat st;
    if (stat(path->data, &st) != 0) return 0;
    return S_ISDIR(st.st_mode) ? 1 : 0;
}

JerryArray* jerry_list_dir(JerryStr* path) {
    JerryArray* result = jerry_array_new(sizeof(void*), 8, 1);
    if (path == NULL) return result;
    struct dirent** entries;
    int n = scandir(path->data, &entries, NULL, alphasort);
    if (n < 0) return result;
    for (int i = 0; i < n; i++) {
        const char* name = entries[i]->d_name;
        if (name[0] != '.') {  /* skip hidden / . / .. */
            JerryStr* s = jerry_string_new(name, (int64_t)strlen(name));
            jerry_array_push(result, &s);
        }
        free(entries[i]);
    }
    free(entries);
    return result;
}

JerryStr* jerry_getenv(JerryStr* name) {
    if (name == NULL) return jerry_string_new("", 0);
    const char* val = getenv(name->data);
    if (val == NULL) return jerry_string_new("", 0);
    return jerry_string_new(val, (int64_t)strlen(val));
}

int64_t jerry_exec(JerryArray* args) {
    if (args == NULL || args->len == 0) {
        fprintf(stderr, "jerry: exec: empty args\n");
        return 1;
    }
    /* Build a null-terminated argv for execvp. */
    char** argv = (char**)malloc(sizeof(char*) * (size_t)(args->len + 1));
    if (!argv) { fprintf(stderr, "jerry: exec: out of memory\n"); return 1; }
    for (int64_t i = 0; i < args->len; i++) {
        JerryStr** slot = (JerryStr**)jerry_array_get(args, i);
        argv[i] = (*slot)->data;
    }
    argv[args->len] = NULL;

    pid_t pid = fork();
    if (pid < 0) {
        free(argv);
        fprintf(stderr, "jerry: exec: fork failed\n");
        return 1;
    }
    if (pid == 0) {
        execvp(argv[0], argv);
        fprintf(stderr, "jerry: exec: cannot execute '%s': %s\n", argv[0], strerror(errno));
        _exit(127);
    }
    free(argv);
    int status = 0;
    waitpid(pid, &status, 0);
    if (WIFEXITED(status)) return (int64_t)WEXITSTATUS(status);
    if (WIFSIGNALED(status)) return (int64_t)(128 + WTERMSIG(status));
    return 1;
}

void jerry_each_line(JerryStr* path, JerryClosure* closure) {
    if (path == NULL || closure == NULL || closure->fn_ptr == NULL) {
        fprintf(stderr, "jerry: each_line: null argument or invalid callback\n");
        exit(1);
    }

    FILE* f = fopen(path->data, "r");
    if (!f) {
        fprintf(stderr, "jerry: each_line: cannot open '%s'\n", path->data);
        exit(1);
    }

    char* line = NULL;
    size_t cap = 0;
    ssize_t len;

    while ((len = getline(&line, &cap, f)) != -1) {
        if (len > 0 && line[len - 1] == '\n') { line[len - 1] = '\0'; len--; }
        if (len > 0 && line[len - 1] == '\r') { line[len - 1] = '\0'; len--; }

        JerryStr* jerry_line = jerry_string_new(line, (int64_t)len);
        closure->fn_ptr(closure->env_ptr, jerry_line);
        jerry_release(jerry_line); /* callback borrows; we own and release */
    }

    free(line);
    fclose(f);
}

/* ── Arrays ─────────────────────────────────────────────────────────────────── */

JerryArray* jerry_array_new(int64_t elem_size, int64_t initial_cap, int8_t heap_elems) {
    JerryArray* arr = jerry_alloc(sizeof(JerryArray));
    JerryHeader* h = (JerryHeader*)arr - 1;
    h->destructor  = jerry_arr_destructor;
    arr->elem_size  = elem_size;
    arr->len        = 0;
    arr->cap        = initial_cap > 0 ? initial_cap : 8;
    arr->heap_elems = heap_elems;
    arr->data       = malloc((size_t)(arr->cap * elem_size));
    if (!arr->data) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    return arr;
}

/* Mark an existing array as holding heap-type elements.  Called by codegen
   when an empty [] literal is annotated with a heap-element type (e.g.
   Token[]) so that push/set/destructor manage reference counts correctly. */
void jerry_array_mark_heap(JerryArray* arr) { arr->heap_elems = 1; }

void* jerry_array_get(JerryArray* arr, int64_t idx) {
    if (idx < 0 || idx >= arr->len) {
        fprintf(stderr,
            "jerry: array index out of bounds: index %lld, len %lld\n",
            (long long)idx, (long long)arr->len);
        exit(1);
    }
    return arr->data + idx * arr->elem_size;
}

void jerry_array_set(JerryArray* arr, int64_t idx, void* elem) {
    if (idx < 0 || idx >= arr->len) {
        fprintf(stderr,
            "jerry: array index out of bounds: index %lld, len %lld\n",
            (long long)idx, (long long)arr->len);
        exit(1);
    }
    if (arr->heap_elems) {
        void* incoming;
        memcpy(&incoming, elem, sizeof(void*));
        void* old;
        memcpy(&old, arr->data + idx * arr->elem_size, sizeof(void*));
        jerry_retain(incoming);
        memcpy(arr->data + idx * arr->elem_size, elem, (size_t)arr->elem_size);
        jerry_release(old);
    } else {
        memcpy(arr->data + idx * arr->elem_size, elem, (size_t)arr->elem_size);
    }
}

int64_t jerry_array_len(JerryArray* arr) {
    return arr->len;
}

void jerry_array_push(JerryArray* arr, void* elem) {
    if (arr->len >= arr->cap) {
        int64_t new_cap = arr->cap * 2;
        char* new_data = realloc(arr->data, (size_t)(new_cap * arr->elem_size));
        if (!new_data) {
            fprintf(stderr, "jerry: array_push: out of memory\n");
            exit(1);
        }
        arr->data = new_data;
        arr->cap  = new_cap;
    }
    if (arr->heap_elems) {
        void* ptr;
        memcpy(&ptr, elem, sizeof(void*));
        jerry_retain(ptr);
    }
    memcpy(arr->data + arr->len * arr->elem_size, elem, (size_t)arr->elem_size);
    arr->len++;
}

/* ── Program arguments ──────────────────────────────────────────────────────── */

static int64_t g_argc = 0;
static char**  g_argv = NULL;

void jerry_capture_args(int64_t argc, char** argv) {
    g_argc = argc;
    g_argv = argv;
}

JerryArray* jerry_args(void) {
    int64_t count = g_argc > 1 ? g_argc - 1 : 0;
    JerryArray* arr = jerry_array_new(sizeof(void*), count > 0 ? count : 1, 1);
    for (int64_t i = 1; i < g_argc; i++) {
        int64_t slen = (int64_t)strlen(g_argv[i]);
        JerryStr* s = jerry_string_new(g_argv[i], slen);
        jerry_array_push(arr, &s);
    }
    return arr;
}

/* Returns JERRY_RUNTIME env var, or <binary_dir>/../lib/jerry_runtime.a.
   Mirrors the same discovery logic as internal/build/build.go:runtimeLibPath(). */
/* exe_dir fills buf (size cap) with the directory containing the running
   executable, with no trailing slash.  Returns 1 on success, 0 on failure.
   Uses OS APIs that work regardless of how argv[0] was set by the shell. */
static int exe_dir(char* buf, size_t cap) {
#if defined(__APPLE__)
    char tmp[4096];
    uint32_t sz = sizeof(tmp);
    if (_NSGetExecutablePath(tmp, &sz) != 0) return 0;
    char resolved[4096];
    if (!realpath(tmp, resolved)) return 0;
    char* slash = strrchr(resolved, '/');
    if (!slash) return 0;
    *slash = '\0';
    snprintf(buf, cap, "%s", resolved);
    return 1;
#elif defined(__linux__)
    char resolved[4096];
    ssize_t len = readlink("/proc/self/exe", resolved, sizeof(resolved) - 1);
    if (len < 0) return 0;
    resolved[len] = '\0';
    char* slash = strrchr(resolved, '/');
    if (!slash) return 0;
    *slash = '\0';
    snprintf(buf, cap, "%s", resolved);
    return 1;
#else
    /* Generic fallback: try argv[0] via realpath */
    if (!g_argv || !g_argv[0]) return 0;
    char resolved[4096];
    if (!realpath(g_argv[0], resolved)) return 0;
    char* slash = strrchr(resolved, '/');
    if (!slash) return 0;
    *slash = '\0';
    snprintf(buf, cap, "%s", resolved);
    return 1;
#endif
}

JerryStr* jerry_runtime_lib_path(void) {
    const char* env = getenv("JERRY_RUNTIME");
    if (env && env[0]) return jerry_string_new(env, (int64_t)strlen(env));
    char dir[4096];
    if (!exe_dir(dir, sizeof(dir))) return jerry_string_new("", 0);
    char path[4096];
    snprintf(path, sizeof(path), "%s/../lib/jerry_runtime.a", dir);
    return jerry_string_new(path, (int64_t)strlen(path));
}

JerryStr* jerry_stdlib_dir_path(void) {
    const char* env = getenv("JERRY_STDLIB");
    if (env && env[0]) return jerry_string_new(env, (int64_t)strlen(env));
    char dir[4096];
    if (!exe_dir(dir, sizeof(dir))) return jerry_string_new("", 0);
    char path[4096];
    snprintf(path, sizeof(path), "%s/../share/jerry/stdlib", dir);
    return jerry_string_new(path, (int64_t)strlen(path));
}

void jerry_flush_stdout(void) {
    fflush(stdout);
}

/* ── I/O extras ─────────────────────────────────────────────────────────────── */

void jerry_print_err(JerryStr* s) {
    if (s == NULL) { fputs("null", stderr); return; }
    fwrite(s->data, 1, (size_t)s->len, stderr);
    fputc('\n', stderr);
}

JerryStr* jerry_read_stdin(void) {
    char* buf = NULL;
    size_t cap = 0, used = 0;
    int c;
    while ((c = fgetc(stdin)) != EOF) {
        if (used + 1 >= cap) {
            size_t new_cap = cap == 0 ? 4096 : cap * 2;
            char* new_buf = realloc(buf, new_cap);
            if (!new_buf) {
                fprintf(stderr, "jerry: read_stdin: out of memory\n");
                exit(1);
            }
            buf = new_buf;
            cap = new_cap;
        }
        buf[used++] = (char)c;
    }
    JerryStr* s = jerry_string_new(buf ? buf : "", (int64_t)used);
    free(buf);
    return s;
}

/* ── Maps ────────────────────────────────────────────────────────────────────── */

static uint64_t map_hash(const void* key, int8_t string_keys) {
    if (string_keys) {
        JerryStr* s;
        memcpy(&s, key, sizeof(JerryStr*));
        /* FNV-1a over string bytes */
        uint64_t h = 14695981039346656037ULL;
        for (int64_t i = 0; i < s->len; i++) {
            h ^= (uint8_t)s->data[i];
            h *= 1099511628211ULL;
        }
        return h;
    } else {
        uint64_t h;
        memcpy(&h, key, 8);
        /* Murmur-inspired finaliser */
        h ^= h >> 33;
        h *= 0xff51afd7ed558ccdULL;
        h ^= h >> 33;
        h *= 0xc4ceb9fe1a85ec53ULL;
        h ^= h >> 33;
        return h;
    }
}

static int map_key_eq(const void* a, const void* b, int8_t string_keys) {
    if (string_keys) {
        JerryStr* sa; JerryStr* sb;
        memcpy(&sa, a, sizeof(JerryStr*));
        memcpy(&sb, b, sizeof(JerryStr*));
        return (int)jerry_string_eq(sa, sb);
    }
    return memcmp(a, b, 8) == 0;
}

static void jerry_map_destructor(void* self) {
    JerryMap* m = (JerryMap*)self;
    for (int64_t i = 0; i < m->bucket_count; i++) {
        JerryMapNode* node = m->buckets[i];
        while (node) {
            JerryMapNode* next = node->next;
            if (m->string_keys) { jerry_release(*(void**)node->key); }
            free(node->value);
            free(node);
            node = next;
        }
    }
    free(m->buckets);
}

JerryMap* jerry_map_new(int8_t string_keys, int64_t value_size) {
    JerryMap* m = jerry_alloc(sizeof(JerryMap));
    JerryHeader* h = (JerryHeader*)m - 1;
    h->destructor = jerry_map_destructor;
    m->bucket_count = 16;
    m->len          = 0;
    m->value_size   = value_size;
    m->string_keys  = string_keys;
    m->buckets = calloc((size_t)m->bucket_count, sizeof(JerryMapNode*));
    if (!m->buckets) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    return m;
}

void jerry_map_set(JerryMap* m, void* key, void* value) {
    uint64_t h   = map_hash(key, m->string_keys);
    int64_t  idx = (int64_t)(h % (uint64_t)m->bucket_count);

    /* Update if key already exists */
    for (JerryMapNode* n = m->buckets[idx]; n; n = n->next) {
        if (map_key_eq(n->key, key, m->string_keys)) {
            memcpy(n->value, value, (size_t)m->value_size);
            return;
        }
    }

    /* Insert new node */
    JerryMapNode* node = malloc(sizeof(JerryMapNode));
    if (!node) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    memcpy(node->key, key, 8);
    if (m->string_keys) { jerry_retain(*(void**)key); }
    node->value = malloc((size_t)m->value_size);
    if (!node->value) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
    memcpy(node->value, value, (size_t)m->value_size);
    node->next       = m->buckets[idx];
    m->buckets[idx]  = node;
    m->len++;

    /* Rehash when load factor exceeds 0.75 */
    if (m->len > m->bucket_count * 3 / 4) {
        int64_t        new_count   = m->bucket_count * 2;
        JerryMapNode** new_buckets = calloc((size_t)new_count, sizeof(JerryMapNode*));
        if (!new_buckets) { fprintf(stderr, "jerry: out of memory\n"); exit(1); }
        for (int64_t i = 0; i < m->bucket_count; i++) {
            JerryMapNode* n = m->buckets[i];
            while (n) {
                JerryMapNode* next = n->next;
                uint64_t nh  = map_hash(n->key, m->string_keys);
                int64_t  ni  = (int64_t)(nh % (uint64_t)new_count);
                n->next          = new_buckets[ni];
                new_buckets[ni]  = n;
                n = next;
            }
        }
        free(m->buckets);
        m->buckets      = new_buckets;
        m->bucket_count = new_count;
    }
}

void* jerry_map_get(JerryMap* m, void* key) {
    uint64_t h   = map_hash(key, m->string_keys);
    int64_t  idx = (int64_t)(h % (uint64_t)m->bucket_count);
    for (JerryMapNode* n = m->buckets[idx]; n; n = n->next) {
        if (map_key_eq(n->key, key, m->string_keys)) {
            return n->value;
        }
    }
    fprintf(stderr, "jerry: map_get: key not found\n");
    exit(1);
}

int8_t jerry_map_has(JerryMap* m, void* key) {
    uint64_t h   = map_hash(key, m->string_keys);
    int64_t  idx = (int64_t)(h % (uint64_t)m->bucket_count);
    for (JerryMapNode* n = m->buckets[idx]; n; n = n->next) {
        if (map_key_eq(n->key, key, m->string_keys)) return 1;
    }
    return 0;
}

void jerry_map_delete(JerryMap* m, void* key) {
    uint64_t       h    = map_hash(key, m->string_keys);
    int64_t        idx  = (int64_t)(h % (uint64_t)m->bucket_count);
    JerryMapNode** prev = &m->buckets[idx];
    for (JerryMapNode* n = *prev; n; prev = &n->next, n = n->next) {
        if (map_key_eq(n->key, key, m->string_keys)) {
            *prev = n->next;
            if (m->string_keys) { jerry_release(*(void**)n->key); }
            free(n->value);
            free(n);
            m->len--;
            return;
        }
    }
}

int64_t jerry_map_len(JerryMap* m) { return m->len; }

JerryArray* jerry_map_keys(JerryMap* m) {
    JerryArray* arr = jerry_array_new(8, m->len > 0 ? m->len : 1, m->string_keys);
    for (int64_t i = 0; i < m->bucket_count; i++) {
        for (JerryMapNode* n = m->buckets[i]; n; n = n->next) {
            jerry_array_push(arr, n->key);
        }
    }
    return arr;
}

/* ── Control ────────────────────────────────────────────────────────────────── */

void jerry_exit(int64_t code) {
    exit((int)code);
}

void jerry_panic(JerryStr* msg) {
    fprintf(stderr, "jerry panic: ");
    if (msg) {
        fwrite(msg->data, 1, (size_t)msg->len, stderr);
    }
    fprintf(stderr, "\n");
    exit(1);
}

/* ── Time ────────────────────────────────────────────────────────────────────── */

int64_t jerry_now_millis(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000 + (int64_t)(ts.tv_nsec / 1000000);
}

int64_t jerry_now_seconds(void) {
    return (int64_t)time(NULL);
}

JerryStr* jerry_now_string(void) {
    time_t t = time(NULL);
    struct tm* tm_info = localtime(&t);
    char buf[32];
    strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", tm_info);
    return jerry_string_new(buf, (int64_t)strlen(buf));
}
