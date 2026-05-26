/**
 * Tree-sitter grammar for the Jerry language.
 *
 * Jerry is a statically-typed, JavaScript-style language that compiles to
 * native binaries via LLVM IR. Source files use the `.jer` extension.
 *
 * Reference: https://github.com/jeffscottbrown/jerry-lang
 */

const PREC = {
  ASSIGN:  1,
  OR:      2,
  AND:     3,
  EQ:      4,
  CMP:     5,
  ADD:     6,
  MUL:     7,
  UNARY:   8,
  POSTFIX: 9,
  CALL:    10,
};

module.exports = grammar({
  name: 'jerry',

  extras: $ => [
    $.line_comment,
    $.block_comment,
    /\s+/,
  ],

  // Treat identifiers as the "word" token so Tree-sitter can correctly
  // distinguish keywords from identifiers in error recovery.
  word: $ => $.identifier,

  // Explicit conflict declarations needed where Tree-sitter cannot resolve
  // ambiguity via precedence alone.
  conflicts: $ => [
    // A bare identifier at the start of a statement could be the beginning
    // of an expression_statement or the name of a function/class/let. The
    // parser resolves this with look-ahead; we declare it here to suppress
    // the warning.
    [$._type, $.type_identifier],
    // '{' at statement level is ambiguous: nested block vs map_expression
    // (inside expression_statement). Tree-sitter resolves via GLR.
    [$.block, $.map_expression],
  ],

  rules: {
    // -------------------------------------------------------------------------
    // Top level
    // -------------------------------------------------------------------------

    source_file: $ => repeat($._top_level_item),

    _top_level_item: $ => choice(
      $.include_declaration,
      $.enum_declaration,
      $.extern_function_declaration,
      $.function_declaration,
      $.class_declaration,
      $.variable_declaration,
      $.expression_statement,
    ),

    // include @stdlib
    // include "github.com/owner/repo"
    include_declaration: $ => seq(
      'include',
      choice(
        seq('@', field('module', $.identifier)),
        field('path', $.string_literal),
      ),
    ),

    // -------------------------------------------------------------------------
    // Enums
    // -------------------------------------------------------------------------

    // enum Direction { North, South, East, West }
    enum_declaration: $ => seq(
      'enum',
      field('name', $.identifier),
      '{',
      optional($.enum_variant_list),
      '}',
    ),

    enum_variant_list: $ => seq(
      $.enum_variant,
      repeat(seq(',', $.enum_variant)),
      optional(','),
    ),

    enum_variant: $ => field('name', $.identifier),

    // -------------------------------------------------------------------------
    // Functions
    // -------------------------------------------------------------------------

    // extern fn name(params): ReturnType;   (no body — declaration only)
    extern_function_declaration: $ => seq(
      'extern',
      'fn',
      field('name', $.identifier),
      '(',
      field('parameters', optional($.parameter_list)),
      ')',
      optional(seq(':', field('return_type', $._type))),
      ';',
    ),

    // fn name(params): ReturnType { body }
    function_declaration: $ => seq(
      'fn',
      field('name', $.identifier),
      '(',
      field('parameters', optional($.parameter_list)),
      ')',
      optional(seq(':', field('return_type', $._type))),
      field('body', $.block),
    ),

    parameter_list: $ => seq(
      $.parameter,
      repeat(seq(',', $.parameter)),
    ),

    parameter: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    // -------------------------------------------------------------------------
    // Classes
    // -------------------------------------------------------------------------

    // class Name extends Base { ... }
    class_declaration: $ => seq(
      'class',
      field('name', $.identifier),
      optional(seq('extends', field('superclass', $.identifier))),
      '{',
      repeat($._class_member),
      '}',
    ),

    _class_member: $ => choice(
      $.field_declaration,
      $.method_declaration,
    ),

    // fieldName: Type;
    field_declaration: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $._type),
      ';',
    ),

    // fn methodName(params): ReturnType { body }
    method_declaration: $ => seq(
      'fn',
      field('name', $.identifier),
      '(',
      field('parameters', optional($.parameter_list)),
      ')',
      optional(seq(':', field('return_type', $._type))),
      field('body', $.block),
    ),

    // -------------------------------------------------------------------------
    // Type expressions
    // -------------------------------------------------------------------------

    _type: $ => choice(
      $.map_type,
      $.array_type,
      $.function_type,
      $.primitive_type,
      $.type_identifier,
    ),

    // Primitive built-in types
    primitive_type: $ => choice('int', 'float', 'bool', 'string', 'void'),

    // User-defined class types
    type_identifier: $ => alias($.identifier, $.type_identifier),

    // ElementType[]  (Jerry only supports single-level postfix array notation)
    array_type: $ => seq(
      field('element', choice($.primitive_type, $.type_identifier)),
      '[]',
    ),

    // map<KeyType, ValueType>
    map_type: $ => seq(
      'map',
      '<',
      field('key', $._type),
      ',',
      field('value', $._type),
      '>',
    ),

    // fn(T1, T2): ReturnType
    function_type: $ => seq(
      'fn',
      '(',
      optional(seq(
        $._type,
        repeat(seq(',', $._type)),
      )),
      ')',
      ':',
      field('return_type', $._type),
    ),

    // -------------------------------------------------------------------------
    // Statements
    // -------------------------------------------------------------------------

    block: $ => seq(
      '{',
      repeat($._statement),
      '}',
    ),

    _statement: $ => choice(
      $.variable_declaration,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
      $.if_statement,
      $.while_statement,
      $.for_statement,
      $.expression_statement,
      $.block,
    ),

    // let name: Type = expr;
    // let name = expr;       (type inferred)
    variable_declaration: $ => seq(
      'let',
      field('name', $.identifier),
      optional(seq(':', field('type', $._type))),
      '=',
      field('value', $._expression),
      ';',
    ),

    return_statement: $ => seq(
      'return',
      optional(field('value', $._expression)),
      ';',
    ),

    break_statement: $ => seq('break', ';'),
    continue_statement: $ => seq('continue', ';'),

    // if cond { } else if cond { } else { }
    if_statement: $ => prec.right(seq(
      'if',
      field('condition', $._expression),
      field('consequence', $.block),
      optional(field('alternative', $.else_clause)),
    )),

    else_clause: $ => seq(
      'else',
      choice($.if_statement, $.block),
    ),

    while_statement: $ => seq(
      'while',
      field('condition', $._expression),
      field('body', $.block),
    ),

    // for (init; cond; update) { }
    // init may be a full variable_declaration (which includes its own `;`)
    // or an optional expression followed by `;`.
    for_statement: $ => seq(
      'for',
      '(',
      choice(
        field('init', $.variable_declaration),
        seq(field('init', optional($._expression)), ';'),
      ),
      field('condition', optional($._expression)),
      ';',
      field('update', optional($._expression)),
      ')',
      field('body', $.block),
    ),

    expression_statement: $ => seq($._expression, ';'),

    // -------------------------------------------------------------------------
    // Expressions
    // -------------------------------------------------------------------------

    _expression: $ => choice(
      $.assignment_expression,
      $.binary_expression,
      $.unary_expression,
      $.update_expression,
      $.call_expression,
      $.index_expression,
      $.member_expression,
      $.new_expression,
      $.function_expression,
      $.array_expression,
      $.map_expression,
      $.parenthesized_expression,
      $.this,
      $.true,
      $.false,
      $.null,
      $.float_literal,
      $.integer_literal,
      $.char_literal,
      $.string_literal,
      $.identifier,
    ),

    // left = right  (right-associative, lowest precedence)
    assignment_expression: $ => prec.right(PREC.ASSIGN, seq(
      field('left', $._expression),
      '=',
      field('right', $._expression),
    )),

    // All binary operators collapsed into one node with per-alternative prec.
    binary_expression: $ => choice(
      prec.left(PREC.OR,  seq(field('left', $._expression), '||', field('right', $._expression))),
      prec.left(PREC.AND, seq(field('left', $._expression), '&&', field('right', $._expression))),
      prec.left(PREC.EQ,  seq(field('left', $._expression), choice('==', '!='), field('right', $._expression))),
      prec.left(PREC.CMP, seq(field('left', $._expression), choice('<', '>', '<=', '>='), field('right', $._expression))),
      prec.left(PREC.ADD, seq(field('left', $._expression), choice('+', '-'), field('right', $._expression))),
      prec.left(PREC.MUL, seq(field('left', $._expression), choice('*', '/', '%'), field('right', $._expression))),
    ),

    // Prefix unary: !expr  -expr
    unary_expression: $ => prec(PREC.UNARY, seq(
      field('operator', choice('!', '-')),
      field('operand', $._expression),
    )),

    // Postfix: expr++  expr--
    update_expression: $ => prec.left(PREC.POSTFIX, seq(
      field('operand', $._expression),
      field('operator', choice('++', '--')),
    )),

    // fn_expr(args)
    call_expression: $ => prec(PREC.CALL, seq(
      field('function', $._expression),
      '(',
      optional($.argument_list),
      ')',
    )),

    argument_list: $ => seq(
      $._expression,
      repeat(seq(',', $._expression)),
    ),

    // expr[index]
    index_expression: $ => prec(PREC.CALL, seq(
      field('object', $._expression),
      '[',
      field('index', $._expression),
      ']',
    )),

    // expr.field
    member_expression: $ => prec(PREC.CALL, seq(
      field('object', $._expression),
      '.',
      field('property', $.identifier),
    )),

    // new ClassName(args)
    new_expression: $ => seq(
      'new',
      field('class', $.identifier),
      '(',
      optional($.argument_list),
      ')',
    ),

    // fn(params): ReturnType { body }
    function_expression: $ => seq(
      'fn',
      '(',
      optional($.parameter_list),
      ')',
      optional(seq(':', field('return_type', $._type))),
      field('body', $.block),
    ),

    // [elem, elem, ...]
    array_expression: $ => seq(
      '[',
      optional(seq(
        $._expression,
        repeat(seq(',', $._expression)),
        optional(','),
      )),
      ']',
    ),

    // { key: value, ... }  or  {}  (empty map)
    map_expression: $ => seq(
      '{',
      optional(seq(
        $.map_entry,
        repeat(seq(',', $.map_entry)),
        optional(','),
      )),
      '}',
    ),

    map_entry: $ => seq(
      field('key', $._expression),
      ':',
      field('value', $._expression),
    ),

    parenthesized_expression: $ => seq('(', $._expression, ')'),

    // Keywords that appear in expression position
    this: $ => 'this',
    true: $ => 'true',
    false: $ => 'false',
    null: $ => 'null',

    // -------------------------------------------------------------------------
    // Literals
    // -------------------------------------------------------------------------

    // Float must take priority over integer so "3.14" lexes as one token.
    float_literal: $ => token(prec(1, /[0-9]+\.[0-9]+/)),
    integer_literal: $ => /[0-9]+/,

    // Single-quoted char literals: 'a', '\n', '\\', etc.
    // The value is the ASCII/Unicode code point (an int at runtime).
    char_literal: $ => token(seq(
      "'",
      choice(
        /[^'\\]/,                 // any single non-escape character
        seq('\\', /[ntr\\'0]/),  // recognised escape sequences
      ),
      "'",
    )),

    // Double-quoted strings with escape sequences.
    string_literal: $ => /"(?:[^"\\]|\\.)*"/,

    // -------------------------------------------------------------------------
    // Identifiers & comments
    // -------------------------------------------------------------------------

    identifier: $ => /[a-zA-Z_][a-zA-Z0-9_]*/,

    line_comment: $ => token(seq('//', /.*/)),

    block_comment: $ => token(seq(
      '/*',
      /[^*]*\*+(?:[^/*][^*]*\*+)*/,
      '/',
    )),
  },
});
