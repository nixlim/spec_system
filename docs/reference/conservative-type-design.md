# Conservative Type Design

**Referenced by:** plan-spec, grill-code (Lens 6), grill-spec review-constitution (CPX-11)

---

## Principle

Do not introduce a user-defined nominal type unless it carries invariants, methods, or domain semantics that the underlying built-in type cannot express.

A wrapper type must justify its existence. If it adds no validation, no behavior, and no meaning beyond what the wrapped type already provides, it is dead weight: it increases surface area, complicates signatures, and forces unnecessary conversions at call boundaries.

---

## Language Examples

### Go

Prefer built-in composite types when no invariants are enforced.

```go
// Preferred
func ProcessNames(names []string) { ... }

// Avoid -- adds indirection with no behavioral payoff
type StringSlice []string
func ProcessNames(names StringSlice) { ... }
```

### Java

Prefer standard library generics over thin inheritance wrappers.

```java
// Preferred
List<User> getActiveUsers() { ... }

// Avoid -- subclassing a collection adds coupling without adding capability
class UserList extends ArrayList<User> { }
UserList getActiveUsers() { ... }
```

### Python

Prefer typed dicts or plain built-in types over dataclasses that wrap a single field.

```python
# Preferred
def aggregate(scores: dict[str, int]) -> int: ...

# Avoid -- a class that exists only to hold one dict
@dataclass
class ScoreMap:
    data: dict[str, int]

def aggregate(scores: ScoreMap) -> int: ...
```

### TypeScript

Prefer built-in utility types over classes that wrap a single structure.

```typescript
// Preferred
function summarize(counts: Record<string, number>): string { ... }

// Avoid -- a class whose only member is the record
class CountMap {
  constructor(public data: Record<string, number>) {}
}
function summarize(counts: CountMap): string { ... }
```

---

## Judgment Calls: Semantic Type Aliases

Type aliases that carry semantic value occupy a gray area and are not automatic violations.

```go
type UserID string
type Meters float64
```

```typescript
type UserID = string & { readonly __brand: unique symbol };
```

These aliases document intent and can prevent accidental misuse (passing a raw string where a user ID is expected). Whether they are warranted depends on the domain:

- **Favor the alias** when the domain has multiple string-typed identifiers that are easy to confuse (UserID vs. SessionID vs. TenantID) or when the alias participates in function signatures across module boundaries.
- **Skip the alias** when the type appears in a narrow scope, the risk of confusion is low, and adding it would inflate the type surface without meaningful safety gain.

The test remains the same: does the alias express something the built-in type alone cannot?

---

## Applying This Principle

When reviewing or planning code:

1. For each new type, ask: what invariant, method, or domain meaning does this add?
2. If the answer is "none," use the built-in type directly.
3. If the answer is "documentation only," consider a type alias instead of a nominal wrapper.
4. If the type enforces constraints (validation on construction, restricted operations, domain logic), it is justified.
