#include "runtime.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ── Memory ─────────────────────────────────────────────────────────────────── */

void* jerry_alloc(int64_t size) {
    void* p = malloc((size_t)size);
    if (!p) {
        fprintf(stderr, "jerry: out of memory\n");
        exit(1);
    }
    memset(p, 0, (size_t)size);
    return p;
}

/* ── Strings ────────────────────────────────────────────────────────────────── */

JerryStr* jerry_string_new(const char* data, int64_t len) {
    JerryStr* s = jerry_alloc(sizeof(JerryStr));
    s->len  = len;
    s->data = jerry_alloc(len + 1);
    if (len > 0 && data != NULL) {
        memcpy(s->data, data, (size_t)len);
    }
    s->data[len] = '\0';
    return s;
}

JerryStr* jerry_string_concat(JerryStr* a, JerryStr* b) {
    int64_t    len = a->len + b->len;
    JerryStr* s   = jerry_alloc(sizeof(JerryStr));
    s->len  = len;
    s->data = jerry_alloc(len + 1);
    memcpy(s->data,          a->data, (size_t)a->len);
    memcpy(s->data + a->len, b->data, (size_t)b->len);
    s->data[len] = '\0';
    return s;
}

int8_t jerry_string_eq(JerryStr* a, JerryStr* b) {
    if (a->len != b->len) return 0;
    return (int8_t)(memcmp(a->data, b->data, (size_t)a->len) == 0);
}

int8_t jerry_string_ne(JerryStr* a, JerryStr* b) {
    return (int8_t)(!jerry_string_eq(a, b));
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

void jerry_print_array(JerryArray* arr) {
    /* Generic array printing — prints raw element bytes as hex.
       Type-specific printing would require runtime type tags.
       For now this is a placeholder; override in Jerry code with
       a manual loop.                                                  */
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
    JerryStr* s = jerry_alloc(sizeof(JerryStr));
    s->len  = (int64_t)size;
    s->data = jerry_alloc(size + 1);
    if (size > 0) {
        size_t n = fread(s->data, 1, (size_t)size, f);
        (void)n;
    }
    s->data[size] = '\0';
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

    // Read file line by line safely handling dynamic string buffers via getline
    while ((len = getline(&line, &cap, f)) != -1) {
        // Strip trailing line configurations (\n and \r)
        if (len > 0 && line[len - 1] == '\n') {
            line[len - 1] = '\0';
            len--;
        }
        if (len > 0 && line[len - 1] == '\r') {
            line[len - 1] = '\0';
            len--;
        }

        // Allocate dynamic JerryStr context for the line read
        JerryStr* jerry_line = jerry_string_new(line, (int64_t)len);

        // Call user closure function passing the environment space pointer and string payload
        closure->fn_ptr(closure->env_ptr, jerry_line);
    }

    free(line);
    fclose(f);
}

/* ── Arrays ─────────────────────────────────────────────────────────────────── */

JerryArray* jerry_array_new(int64_t elem_size, int64_t initial_cap) {
    JerryArray* arr = jerry_alloc(sizeof(JerryArray));
    arr->elem_size = elem_size;
    arr->len       = 0;
    arr->cap       = initial_cap > 0 ? initial_cap : 8;
    arr->data      = jerry_alloc(arr->cap * elem_size);
    return arr;
}

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
    memcpy(arr->data + idx * arr->elem_size, elem, (size_t)arr->elem_size);
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
    JerryArray* arr = jerry_array_new(sizeof(void*), count > 0 ? count : 1);
    for (int64_t i = 1; i < g_argc; i++) {
        int64_t slen = (int64_t)strlen(g_argv[i]);
        JerryStr* s = jerry_string_new(g_argv[i], slen);
        jerry_array_push(arr, &s);
    }
    return arr;
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
