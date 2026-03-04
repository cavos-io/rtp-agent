# **Hexagonal Architecture Directory Structure Guide**

```text
/adapter                  # External Adapters Layer - connects core to external systems
  /postgresql             # Database adapter for PostgreSQL persistence
    /entities             # Database entity mappings and ORM models
    A_repository.go       # Repository implementation for domain A
    B_repository.go       # Repository implementation for domain B
  /util                   # Adapter-specific utility functions
    A_util.go             # Helper functions for adapter A operations
    B_util.go             # Helper functions for adapter B operations
  /kafka_producer         # Message broker adapter for event publishing
/cmd                      # Application Entry Point - bootstrap and config
  properties.yaml         # Environment-specific configuration settings
  main.go                 # Application entry point and dependency injection
/core                     # Business Logic Layer (The Hexagon) - pure domain logic
  /serviceA               # Domain service A with business rules
    service.go            # Core business logic implementation
    servie_test.go        # Unit tests for business logic
    repository.go         # Port interface for data access
    util.go               # Domain-specific utility functions
    event.go              # Domain events and event handling
  /serviceB               # Domain service B with business rules
    service.go            # Core business logic implementation
    servie_test.go        # Unit tests for business logic
    repository.go         # Port interface for data access
    util.go               # Domain-specific utility functions
    event.go              # Domain events and event handling
/db                       # Database Management - schema and data
  /migration              # Database schema version control
  /seed                   # Test data and initial database setup
/interface                # Input Adapters (Primary) - external entry points
  /web                    # HTTP REST API adapter
  /kafka_consumer         # Message consumer adapter for incoming events
  /grpc                   # gRPC service adapter for RPC calls
  /ws                     # WebSocket adapter for real-time communication
  /worker                 # Background job processing adapter
/libary                   # Shared Libraries - reusable components
/model                    # Data Models - shared data structures and DTOs
  A_model.go              # Domain models and DTOs for service A
  B_model.go              # Domain models and DTOs for service B
.gitignore                # Git ignore file
.dockerignore             # Docker ignore file
app.go                    # Application orchestration and setup
Dockerfile                # Container configuration
README.md                 # Project documentation
```

## Overview

This project follows hexagonal architecture (ports and adapters pattern) designed for modularity and adaptability. The structure separates business logic from external concerns through well-defined interfaces.

## Directory Breakdown

### `/adapter` - External Adapters Layer

Contains implementations that connect the core business logic to external systems. These are the "adapters" in hexagonal architecture.

#### `/postgresql`

* **Purpose**: Database adapter for PostgreSQL
* **Contents**:
  * `/entities`: Database entity mappings and ORM models
  * `A_repository.go`, `B_repository.go`: Concrete implementations of repository interfaces defined in core
* **Role**: Implements data persistence port from the core domain

#### `/util`

* **Purpose**: Adapter-specific utility functions
* **Contents**: `A_util.go`, `B_util.go` - Helper functions for adapter operations
* **Role**: Supporting utilities for external system integrations

#### `/kafka_producer`

* **Purpose**: Message broker adapter for publishing events
* **Role**: Implements event publishing port from the core domain

### `/cmd` - Application Entry Point

* **Purpose**: Application bootstrap and configuration
* **Contents**:
  * `main.go`: Application entry point and dependency injection
  * `properties.yaml`: Configuration file for environment-specific settings
* **Role**: Orchestrates the hexagon by wiring up all components

### `/core` - Business Logic Layer (The Hexagon)

The heart of the hexagonal architecture containing pure business logic without external dependencies.

#### `/serviceA` & `/serviceB`

* **Purpose**: Domain services encapsulating business rules
* **Contents**:
  * `service.go`: Business logic implementation
  * `service_test.go`: Unit tests for business logic
  * `repository.go`: Port interface for data access
  * `util.go`: Domain-specific utility functions
  * `event.go`: Domain events and event handling
* **Role**: Contains the core business rules and defines ports (interfaces) for external interactions

### `/db` - Database Management

* **Purpose**: Database schema and data management
* **Contents**:
  * `/migration`: Database schema migrations
  * `/seed`: Test data and initial database seeding scripts
* **Role**: Database version control and initial data setup

### `/interface` - Input Adapters (Primary Adapters)

Contains various ways external systems can interact with your application.

#### `/web`

* **Purpose**: HTTP REST API adapter
* **Role**: Handles HTTP requests and translates them to business operations

#### `/kafka_consumer`

* **Purpose**: Message consumer adapter
* **Role**: Processes incoming messages from Kafka topics

#### `/grpc`

* **Purpose**: gRPC service adapter
* **Role**: Provides RPC interface for the application

#### `/ws`

* **Purpose**: WebSocket adapter
* **Role**: Handles real-time bidirectional communication

#### `/worker`

* **Purpose**: Background job processing adapter
* **Role**: Handles asynchronous task processing

### `/library` - Shared Libraries

* **Purpose**: Reusable components and utilities
* **Role**: Common functionality that can be used across different parts of the application

### `/model` - Data Models

* **Purpose**: Shared data structures and DTOs
* **Contents**: `A_model.go`, `B_model.go` - Domain models and data transfer objects
* **Role**: Defines the data contracts used throughout the application

## Key Principles Achieved

### 1. Modularity

* Each service (A, B) is self-contained with its own business logic
* Adapters are interchangeable without affecting core logic
* Clear separation between layers prevents tight coupling

### 2. Adaptability

* Multiple interface types (web, gRPC, WebSocket, etc.) can coexist
* Database can be switched by implementing new repository adapters
* Message brokers can be changed without core logic modification
* New interfaces can be added without modifying existing code

### 3. Testability

* Core business logic can be tested independently
* Adapters can be mocked easily due to interface-based design
* Each layer has its own test strategy

### 4. Dependency Direction

* Core defines interfaces (ports)
* Adapters implement these interfaces
* Dependencies point inward toward the core
* External systems depend on your interfaces, not vice versa

## Flow Example

1. HTTP request comes to `/interface/web`
2. Web adapter calls core service methods
3. Core service uses repository port (interface)
4. Repository implementation in `/adapter/postgresql` handles data persistence
5. Events are published through `/adapter/kafka_producer`
6. Response flows back through the layers

This structure ensures that your business logic remains pure and testable while providing maximum flexibility for integrating with external systems.