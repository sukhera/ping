"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { sessionKeys } from "@/hooks/use-session";
import { ApiError, register } from "@/lib/api";

const registerSchema = z.object({
  email: z.string().email("Enter a valid email address."),
  password: z.string().min(12, "Password must be at least 12 characters."),
});

type RegisterValues = z.infer<typeof registerSchema>;

export default function RegisterPage() {
  const router = useRouter();
  const queryClient = useQueryClient();

  const form = useForm<RegisterValues>({
    resolver: zodResolver(registerSchema),
    mode: "onBlur",
    defaultValues: { email: "", password: "" },
  });

  const mutation = useMutation({
    mutationFn: (values: RegisterValues) =>
      register(values.email, values.password),
    onSuccess: (data) => {
      queryClient.setQueryData(sessionKeys.all, data.user);
      router.push("/dashboard");
    },
  });

  // Per docs/DEVELOPMENT.md: the raw "registration is closed" string is an
  // internal API message, not something to surface verbatim to users.
  const errorMessage =
    mutation.error instanceof ApiError
      ? mutation.error.status === 403
        ? "Registration is currently closed."
        : mutation.error.status === 409
          ? "An account with this email already exists."
          : mutation.error.status === 429
            ? "Too many attempts. Try again shortly."
            : mutation.error.message
      : mutation.error
        ? "Unable to reach the server."
        : null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Create an account</CardTitle>
        <CardDescription>Get started with ping.</CardDescription>
      </CardHeader>
      <CardContent>
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            className="flex flex-col gap-4"
          >
            <FormField
              control={form.control}
              name="email"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Email</FormLabel>
                  <FormControl>
                    <Input type="email" autoComplete="email" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Password</FormLabel>
                  <FormControl>
                    <Input
                      type="password"
                      autoComplete="new-password"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            {errorMessage && (
              <p role="alert" className="text-sm text-down">
                {errorMessage}
              </p>
            )}
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? "Creating account…" : "Create account"}
            </Button>
          </form>
        </Form>
        <p className="mt-4 text-sm text-text-dim">
          Already have an account?{" "}
          <Link href="/login" className="text-accent underline">
            Log in
          </Link>
        </p>
      </CardContent>
    </Card>
  );
}
