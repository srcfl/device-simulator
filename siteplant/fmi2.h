#ifndef SITEPLANT_FMI2_H
#define SITEPLANT_FMI2_H

#include <stddef.h>

typedef void* fmi2Component;
typedef void* fmi2ComponentEnvironment;
typedef void* fmi2FMUstate;
typedef unsigned int fmi2ValueReference;
typedef double fmi2Real;
typedef int fmi2Integer;
typedef int fmi2Boolean;
typedef char fmi2Char;
typedef const fmi2Char* fmi2String;
typedef char fmi2Byte;

typedef enum {
	fmi2OK,
	fmi2Warning,
	fmi2Discard,
	fmi2Error,
	fmi2Fatal,
	fmi2Pending
} fmi2Status;

typedef enum {
	fmi2ModelExchange,
	fmi2CoSimulation
} fmi2Type;

#define fmi2True 1
#define fmi2False 0

typedef void (*fmi2CallbackLogger)(fmi2ComponentEnvironment, fmi2String, fmi2Status, fmi2String, fmi2String, ...);
typedef void* (*fmi2CallbackAllocateMemory)(size_t, size_t);
typedef void (*fmi2CallbackFreeMemory)(void*);
typedef void (*fmi2StepFinished)(fmi2ComponentEnvironment, fmi2Status);

typedef struct {
	fmi2CallbackLogger logger;
	fmi2CallbackAllocateMemory allocateMemory;
	fmi2CallbackFreeMemory freeMemory;
	fmi2StepFinished stepFinished;
	fmi2ComponentEnvironment componentEnvironment;
} fmi2CallbackFunctions;

typedef fmi2Component (*fmi2InstantiateTYPE)(fmi2String, fmi2Type, fmi2String, fmi2String, const fmi2CallbackFunctions*, fmi2Boolean, fmi2Boolean);
typedef void (*fmi2FreeInstanceTYPE)(fmi2Component);
typedef fmi2Status (*fmi2SetupExperimentTYPE)(fmi2Component, fmi2Boolean, fmi2Real, fmi2Real, fmi2Boolean, fmi2Real);
typedef fmi2Status (*fmi2EnterInitializationModeTYPE)(fmi2Component);
typedef fmi2Status (*fmi2ExitInitializationModeTYPE)(fmi2Component);
typedef fmi2Status (*fmi2DoStepTYPE)(fmi2Component, fmi2Real, fmi2Real, fmi2Boolean);
typedef fmi2Status (*fmi2SetRealTYPE)(fmi2Component, const fmi2ValueReference[], size_t, const fmi2Real[]);
typedef fmi2Status (*fmi2GetRealTYPE)(fmi2Component, const fmi2ValueReference[], size_t, fmi2Real[]);
typedef fmi2Status (*fmi2TerminateTYPE)(fmi2Component);
typedef fmi2Status (*fmi2ResetTYPE)(fmi2Component);

#endif
