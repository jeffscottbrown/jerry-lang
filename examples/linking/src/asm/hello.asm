; hello.asm — prints "Hello From NASM" by calling puts().
;
; Built by the Makefile with the correct format flag:
;   macOS:  nasm -f macho64 -DMACHO hello.asm
;   Linux:  nasm -f elf64           hello.asm
;
; On macOS all external symbols carry a leading underscore (_); the MACHO
; define selects the right names without duplicating the function body.

%ifdef MACHO
    %define FN_NAME  _hello_from_nasm
    %define PUTS     _puts
%else
    %define FN_NAME  hello_from_nasm
    %define PUTS     puts
%endif

section .data
    msg db "Hello From NASM", 0   ; null-terminated string (puts adds \n)

section .text
    global FN_NAME
    extern PUTS

FN_NAME:
    push    rbp
    mov     rbp, rsp
    and     rsp, -16            ; 16-byte stack alignment (ABI requirement)
    lea     rdi, [rel msg]      ; first argument: pointer to msg
    call    PUTS                ; puts(msg) — prints msg + newline
    pop     rbp
    ret
