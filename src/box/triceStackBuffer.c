//! \file triceStackBuffer.c
//! \author Thomas.Hoehenleitner [at] seerose.net
//! //////////////////////////////////////////////////////////////////////////
#include "trice.h"

#if TRICE_DIRECT_BUFFER == TRICE_STACK_BUFFER

#ifdef TRICE_CGO
void TriceTransfer( void ){} // todo 
#endif

#endif // #if TRICE_MODE == TRICE_STACK_BUFFER
