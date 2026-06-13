# Anki-like System — Functional Requirements Specification

## 1. Overview

This document defines the functional requirements for a spaced repetition learning system similar to Anki.

The system is intended for efficient long-term memorization using adaptive scheduling algorithms, flexible content modeling, and multi-device synchronization.

---

## 2. Core Concepts

### 2.1 Note

A Note is a structured data entity containing multiple fields.

* A single Note can generate multiple Cards.
* Notes are based on a Note Type (schema).

---

### 2.2 Card

A Card is a unit of learning derived from a Note.

* Contains:

  * Front (question)
  * Back (answer)
* Cards are scheduled independently.

---

### 2.3 Deck

A Deck is a collection of Cards.

* Supports:

  * Nested decks
  * Independent settings per deck

---

## 3. Card Creation & Editing

### 3.1 Basic Card Types

#### 3.1.1 Basic

* Front → Back mapping

#### 3.1.2 Basic (Reversed)

* Generates:

  * A → B
  * B → A

#### 3.1.3 Cloze Deletion

* Supports inline hidden text:

```
Python was created by {{c1::Guido van Rossum}}
```

#### 3.1.4 Image Occlusion

* Ability to mask regions of an image
* Used for diagrams, anatomy, maps

---

### 3.2 Fields

Notes must support multiple fields:

Example:

* Term
* Definition
* Example
* Audio
* Metadata

---

### 3.3 Media Support

The system must support:

* Images
* Audio (auto-play option)
* Video
* LaTeX (math rendering)

---

### 3.4 Formatting

Cards must support:

* HTML
* CSS styling
* Optional JavaScript execution

---

## 4. Learning System

### 4.1 Card States

Cards must be categorized as:

* New
* Learning
* Review
* Relearning

---

### 4.2 User Feedback

After showing answer, user must select:

* Again
* Hard
* Good
* Easy

---

### 4.3 Scheduling Algorithm

#### 4.3.1 Core Requirements

* Adaptive spaced repetition algorithm
* Based on user performance history

#### 4.3.2 FSRS Support

* Desired retention parameter (e.g. 85–95%)
* Dynamic interval calculation
* Load simulation capability

---

### 4.4 Daily Limits

User-configurable:

* New cards per day
* Review limit per day

---

## 5. Organization & Management

### 5.1 Tags

* Cards/Notes must support multiple tags
* Used for filtering and search

---

### 5.2 Flags

* Color-based markers:

  * Red, Orange, Green, Blue

---

### 5.3 Custom Note Types

Users must be able to:

* Define schemas
* Configure card templates

---

## 6. Search & Browsing

### 6.1 Browser Interface

Must support:

* Viewing all cards
* Editing
* Deleting
* Moving cards

---

### 6.2 Advanced Search

Query support:

* deck:<name>
* tag:<name>
* is:new / is:review
* flag:<color>

---

### 6.3 Bulk Operations

* Mass edit fields
* Add/remove tags
* Move between decks

---

## 7. Filtered Decks

System must allow dynamic decks based on queries.

Examples:

* Difficult cards
* Cards with specific tags
* Pre-exam review set

---

## 8. Statistics & Analytics

System must provide:

* Daily activity
* Review count
* Accuracy rate
* Time spent
* Retention estimation

Optional:

* Heatmap visualization

---

## 9. Synchronization

### 9.1 Cloud Sync

* Multi-device sync
* Conflict resolution

---

### 9.2 Supported Platforms

* Desktop (Windows, macOS, Linux)
* Mobile (iOS, Android)
* Web

---

### 9.3 Media Sync

* Images
* Audio
* Video

---

### 9.4 Auto Sync

* On startup
* On exit

---

## 10. Import / Export

Supported formats:

* CSV
* JSON
* Native backup format

---

### 10.1 Shared Decks

* Ability to import external decks
* Community-driven content

---

## 11. Extensibility

### 11.1 Plugin System

* Support third-party extensions
* API for plugins

Examples:

* TTS integration
* Enhanced statistics
* UI improvements

---

## 12. Advanced Features

### 12.1 Template Engine

* Conditional rendering
* Field-based logic

---

### 12.2 JavaScript Support

* Interactive cards
* Dynamic hints
* Timers

---

### 12.3 Multiple Profiles

* Separate datasets per user context

---

### 12.4 Backups

* Automatic periodic backups
* Restore capability

---

### 12.5 Self-hosted Sync (Optional)

* Custom sync server support

---

## 13. Priority Features (MVP → Advanced)

### 13.1 MVP

* Basic cards
* Decks
* Scheduling (simplified)
* Daily limits
* Sync

---

### 13.2 Intermediate

* Cloze deletion
* Tags & search
* Statistics
* Media support

---

### 13.3 Advanced

* FSRS algorithm
* Image occlusion
* Plugin system
* Custom templates
* Filtered decks

---

## 14. Non-Functional Considerations

* Performance with large datasets (100k+ cards)
* Offline-first architecture
* Data integrity and backups
* Cross-platform consistency
* Extensibility

---

## 15. Target Use Cases

* Language learning
* Medical education
* Technical knowledge retention
* Exam preparation
* Personal knowledge management

---
