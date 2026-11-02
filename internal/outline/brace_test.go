package outline

import (
	"fmt"
	"strings"
	"testing"
)

// showK 和 show 一样，名字后面多写种类的一个字母（F 函数、M 方法、T 类型、C 类、S 分组）。
func showK(syms []Symbol) string {
	var b strings.Builder
	var walk func(syms []Symbol, depth int)
	walk = func(syms []Symbol, depth int) {
		for _, s := range syms {
			b.WriteString(strings.Repeat("  ", depth) + s.Name)
			if s.Detail != "" {
				b.WriteString(" [" + s.Detail + "]")
			}
			fmt.Fprintf(&b, " %c @%d\n", "FMTKVCS"[s.Kind-Func], s.Line)
			walk(s.Children, depth+1)
		}
	}
	walk(syms, 0)
	return b.String()
}

// checkK 解析 name 这个文件的 src（去掉开头的换行，第一行是第 0 行），比 showK。
func checkK(t *testing.T, name, src, want string) {
	t.Helper()
	if got := showK(Parse(name, strings.TrimPrefix(src, "\n"))); got != strings.TrimPrefix(want, "\n") {
		t.Errorf("%s 的大纲：\n%s\n应为：\n%s", name, got, strings.TrimPrefix(want, "\n"))
	}
}

// 六种按花括号的语言认哪些扩展名：照 mygo 的 LanguageFor，Objective-C（.m .mm）、Ruby、PHP、Lua 不认。
func TestBraceSupported(t *testing.T) {
	for _, n := range []string{"a.c", "a.h", "a.cpp", "a.cc", "a.cxx", "a.hpp", "a.hh", "a.hxx", "a.ino", "A.java", "a.kt", "a.kts", "a.swift", "a.cs", "a.csx", "A.CPP"} {
		if !Supported(n) {
			t.Errorf("%s 该有大纲", n)
		}
	}
	for _, n := range []string{"a.m", "a.mm", "a.rb", "a.php", "a.lua"} {
		if Supported(n) {
			t.Errorf("%s 不该有大纲", n)
		}
	}
}

// C：只列定义（原型、宏、函数体里的都不要），返回类型在上一行的也认；#if 0 只跟 #else 那边，字符串、字符、注释里的花括号不数；
// 顶格的 TEST(…) { 这种宏块里面的原型也不算。
func TestParseC(t *testing.T) {
	checkK(t, "a.c", `
#include <stdio.h>
#define MAX(a, b) ((a) > (b) ? (a) : (b))
#define BODY { \
    x++; \
}

struct point {
    int x, y;
};

typedef struct node {
    struct node *next;
} node_t;

enum color { RED, GREEN };

int add(int a, int b); /* 原型 */

static int
helper(int x)
{
    return x + 1;
}

int main(int argc, char **argv) {
    if (argc > 1) {
        printf("{ not a brace\n");
    }
    call(a,
         b(c));
    /* int fake(void) { */
    return add(1, 2);
}

#if 0
int disabled(void) {
#else
int enabled(void) {
#endif
    return 0;
}

TEST(Foo, Bar) {
    int local(void);
}

char c = '{';
void (*(get_handler(void)))(int) { return 0; }
struct point origin = { 0, 0 };

void last(void) {}
`, `
point [struct] T @6
node [struct] T @10
color [enum] T @14
helper F @19
main F @24
enabled F @37
last F @50
`)
}

// C++：命名空间、类嵌套，类里的声明（构造、析构、operator、成员函数）都列，类外的 Foo::bar 定义是 Method、Detail 写类名；
// 前置声明、友元、宏、成员初始化里的调用、lambda 不算；extern "C" { 里面照常认；数字分隔 1'000 不当字符。
func TestParseCpp(t *testing.T) {
	checkK(t, "a.cpp", `
#include <vector>
namespace app {
namespace detail { int hidden(); }

template <typename T>
class Stack : public Base<T> {
public:
    Stack();
    explicit Stack(int n) : n_(n) {}
    ~Stack();
    void push(const T& v);
    bool operator==(const Stack& o) const;
    struct Node {
        Node* next;
    };
    Q_OBJECT
    friend void swap(Stack&, Stack&);
private:
    int n_ = compute(1,
                     2);
};

enum class Color : unsigned char { Red, Green };
class Forward;

std::string Stack::name() const
{
    auto f = [](int x) { return x; };
    return std::string("}");
}

Stack::Stack()
    : n_(0) {
}

}  // namespace app

extern "C" {
int c_api(void) { return 0; }
}

int main() {
    std::vector<int> v(10);
    else_if();
}

struct Sep {
    int arr[2] = { 1'000, 2 };
    void after();
};
void outside() {}
`, `
app [namespace] S @1
  detail [namespace] S @2
  Stack [class] C @5
    Stack M @7
    Stack M @8
    ~Stack M @9
    push M @10
    operator== M @11
    Node [struct] T @12
  Color [enum class] T @22
  name [Stack] M @25
  Stack [Stack] M @31
c_api F @38
main F @41
Sep [struct] T @46
  after M @48
outside F @50
`)
	// .h 用 C++ 的规则：C 的头文件只有类型和内联函数（原型不列）
	checkK(t, "a.h", `
#ifndef A_H
#define A_H
typedef struct { int x; } anon_t;
struct buf { char *p; };
int buf_len(const struct buf *b);
static inline int buf_empty(const struct buf *b) { return b->p == 0; }
#endif
`, `
buf [struct] T @3
buf_empty F @5
`)
}

// Java：类里的构造、方法（多行的签名、throws 在下一行），接口里没有方法体的也列；字段初始化里续行的调用、
// 匿名类和 lambda、方法里的局部类、静态块、文本块里像方法的字都不算。
func TestParseJava(t *testing.T) {
	checkK(t, "Shop.java", `
package com.example;

import java.util.List;

@Deprecated
public final class Shop<T> extends Base
        implements Runnable {
    private static final List<String> NAMES = Arrays.asList(
        "a",
        helper("b"));
    private int count;

    public Shop(int count) {
        this.count = count;
    }

    @Override
    public void run() {
        new Thread(() -> {
            doIt();
        }).start();
        if (count > 0) {
            class Local { void hidden() {} }
        }
    }

    public static <K> Map<K, List<T>> group(
            List<T> items) throws IOException {
        return null;
    }

    abstract String name();

    enum Kind { A, B; void f() {} }

    interface Listener {
        void onEvent(String e);
    }

    record Point(int x, int y) {}

    static {
        String s = """
            void notAMethod() {
            """;
    }
}
`, `
Shop [class] C @5
  Shop M @12
  run M @17
  group M @26
  name M @31
  Kind [enum] T @33
  Listener [interface] C @35
    onEvent M @36
  Point [record] C @39
`)
}

// Kotlin：类的主构造写成好几行、companion object（没名字的叫 Companion）、次构造、反引号的名字、扩展函数；
// 顶层的 val / var、init 块、函数里的局部函数和 object 表达式、原始字符串都不算。
func TestParseKotlin(t *testing.T) {
	checkK(t, "a.kt", `
package app

val top = 1
var counter = compute(
    2,
)

data class User(
    val name: String,
    val age: Int,
) : Base() {
    fun greet(): String = "hi $name"

    companion object {
        fun create(): User = User("a", 1)
    }

    constructor(name: String) : this(name, 0)

    init {
        fun local() {}
    }
}

object Registry {
    val map = mutableMapOf<String, Int>()
    private fun `+"`weird name`"+`(x: Int) {}
}

fun <T> List<T>.second(): T {
    val o = object : Runnable {
        override fun run() {}
    }
    return this[1]
}

interface Shape {
    fun area(): Double
}

sealed class Result {
    companion object Factory
}

val s = """
    fun fake() {
"""
`, `
User [class] C @7
  greet M @11
  Companion [companion object] C @13
    create M @14
  constructor M @17
Registry [object] C @24
  weird name M @26
second F @29
Shape [interface] C @36
  area M @37
Result [class] C @40
  Factory [companion object] C @41
`)
}

// Swift：protocol / struct / extension / actor / enum，init、deinit、运算符函数；计算属性、class var、
// 函数里的局部函数、#if 的另一个分支、#Preview 块、多行字符串都不算。
func TestParseSwift(t *testing.T) {
	checkK(t, "a.swift", `
import SwiftUI

protocol Drawable {
    func draw()
}

struct Circle: Drawable, Equatable {
    var radius: Double
    var area: Double {
        return .pi * radius * radius
    }
    init(radius: Double) {
        self.radius = radius
    }
    func draw() {
        func helper() {}
        print("}")
    }
    static func ==(lhs: Circle, rhs: Circle) -> Bool { true }
    class var shared: Circle { Circle(radius: 1) }
}

extension Circle: CustomStringConvertible {
    var description: String { "circle" }
}

#if os(iOS)
final class ViewController: UIViewController {
#else
final class ViewController: NSViewController {
#endif
    override func viewDidLoad() {}
    deinit {}
}

@MainActor
actor Counter {
    private(set) var value = 0
}

enum Direction: String {
    case north, south
}

#Preview {
    ContentView()
}

let text = """
    func fake() {
    """

func free<T>(_ x: T) where T: Hashable {
}
`, `
Drawable [protocol] C @2
  draw M @3
Circle [struct] T @6
  init M @11
  draw M @14
  == M @18
Circle [extension] S @22
ViewController [class] C @27
  viewDidLoad M @31
  deinit M @32
Counter [actor] C @36
Direction [enum] T @40
free F @52
`)
}

// C#：文件级命名空间（之后的都在它里面）、构造 / 终结器、泛型方法带 where、显式接口实现、记录；属性、表达式属性、
// 字段初始化的续行、逐字字符串里的花括号、局部函数、#region 都不算。
func TestParseCSharp(t *testing.T) {
	checkK(t, "a.cs", `
using System;
using System.Collections.Generic;

namespace Shop.Core;

[Serializable]
public sealed partial class Order : IOrder
{
    private readonly List<int> _items = new List<int>(
        capacity: 4);
    public int Count { get; set; }
    public string Name => _name;

    public Order(int id) : base(id)
    {
        var path = @"C:\{dir}\";
        void Local() { }
    }

    ~Order() { }

    public async Task<int> LoadAsync<T>(CancellationToken ct) where T : class
    {
        if (ct.IsCancellationRequested) { return 0; }
        return await Task.FromResult(1);
    }

    int IOrder.Total() => 0;

    public record Line(string Sku, int Qty);

    #region Helpers
    private static bool Check(string s) { return s != "{"; }
    #endregion
}

public interface IOrder
{
    int Total();
}

public enum State { Open, Closed }

internal record struct Money(decimal Amount);
`, `
Shop.Core [namespace] S @3
  Order [class] C @6
    Order M @13
    ~Order M @19
    LoadAsync M @21
    IOrder.Total M @27
    Line [record] C @29
    Check M @32
  IOrder [interface] C @36
    Total M @38
  State [enum] T @41
  Money [record struct] C @43
`)
	// 块式的命名空间：嵌套，关上之后的在顶层
	checkK(t, "b.cs", `
namespace A {
    namespace B {
        class C { }
    }
}
class D { }
`, `
A [namespace] S @0
  B [namespace] S @1
    C [class] C @2
D [class] C @5
`)
}
